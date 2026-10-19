// Package daemon owns the pad. Each Claude Code session attaches to it for as
// long as the session lives, and gets a key, a light and the pad's controls.
//
// The daemon serves HTTP on a Unix socket:
//
//	POST /attach                 attach a session; the response streams what
//	                             the daemon tells it, one JSON object per line,
//	                             for as long as the session lives
//	POST /sessions/{id}/state    set a session's state: {"state": "working"}
//	POST /sessions/{id}/ask      wait for the pad to answer the session's
//	                             permission prompt: {"answer": "allow"},
//	                             "deny", or null once the prompt is gone
//	GET  /status                 the pad and each session, for doctor
package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/desktop"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/pad"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/terminals"
)

// Protocol is the version of the API above. A client speaking another is
// turned away; between versions of the same protocol the newest daemon wins.
const Protocol = 1

const slots = 6

// States a session can be in.
const (
	Idle     = "idle"
	Working  = "working"
	Awaiting = "awaiting" // on a permission prompt
	Unread   = "unread"   // finished, with an answer not yet looked at
	Error    = "error"
)

// Endpoint is where the plugin reaches the daemon: a Unix socket, or on
// Windows a loopback URL with a secret to present.
type Endpoint struct {
	Socket string `json:"socket,omitempty"`
	URL    string `json:"url,omitempty"`
	Token  string `json:"token,omitempty"`
}

// Origin is the app the daemon was started from, and the apps already found
// blocked from the pad. On macOS a daemon reaches the pad with the
// permissions of the app it started under, so a blocked one makes way for
// one started from another (see handOff).
type Origin struct {
	App     string
	Blocked []string
}

// Hello is what a session attaches with.
type Hello struct {
	PID      int               `json:"pid"`
	CWD      string            `json:"cwd"`
	Env      map[string]string `json:"env"`
	Slot     *int              `json:"slot"`           // the key it had, to keep it across restarts
	Claude   string            `json:"claude_session"` // Claude Code's id for the session
	App      string            `json:"app"`            // the app the session runs under
	Version  string            `json:"version"`
	Protocol int               `json:"protocol"`
}

// Session is an attached Claude Code session.
type Session struct {
	id      string
	claude  string // Claude Code's id for the session, which /clear changes
	app     string // the app it runs under
	pid     int
	cwd     string
	env     map[string]string
	state   string
	slot    int       // -1 for none
	since   time.Time // when it entered its state
	started time.Time // when you last started a turn in it

	// Where keys go: a pane (see terminals) or a desktop window. And the
	// desktop window around it, to raise it and to tell when it has focus.
	window, gui string
	locate      sync.Once
	located     chan struct{} // closed once window and gui are known

	// The permission prompt waiting on the pad, if any: the dialog shows one
	// at a time, so a newer one replaces it.
	ask chan string

	out  chan []byte
	done chan struct{} // closed to end the session's stream
	end  sync.Once
}

func (s *Session) close() { s.end.Do(func() { close(s.done) }) }

// DeviceInfo is what sessions are told of the pad.
type DeviceInfo struct {
	Connected bool   `json:"connected"`
	Model     string `json:"model,omitempty"`
	Transport string `json:"transport,omitempty"`
	Firmware  string `json:"firmware,omitempty"`
	Battery   *int   `json:"battery,omitempty"`
	Charging  bool   `json:"charging,omitempty"`
	Problem   string `json:"problem,omitempty"` // why it could not be opened
}

// Daemon is the running daemon.
type Daemon struct {
	version    string
	dir        string
	origin     Origin
	endpoint   Endpoint
	configPath string
	desktop    desktop.Desktop
	log        *log.Logger
	dirty      chan struct{} // the lights need sending
	exit       func()        // ends the process, to make way for another daemon

	mu          sync.Mutex // guards everything below
	sessions    map[string]*Session
	device      *pad.Device
	info        DeviceInfo
	notAllowed  bool     // macOS keeps the pad from this daemon
	focused     *Session // the session whose window has focus
	last        *Session // the session a control last went to
	config      config.Config
	configStamp int64
	controls    // the pad's controls, mid-gesture
}

// New makes a daemon that will listen in dir.
func New(version, dir string, origin Origin, d desktop.Desktop, logger *log.Logger) *Daemon {
	path := config.File()
	c, problems := config.Load(path)
	for _, p := range problems {
		logger.Println("config:", p)
	}
	return &Daemon{
		version:     version,
		dir:         dir,
		origin:      origin,
		configPath:  path,
		desktop:     d,
		log:         logger,
		dirty:       make(chan struct{}, 1),
		exit:        func() { os.Exit(0) },
		sessions:    map[string]*Session{},
		config:      c,
		configStamp: config.Modified(path),
		controls:    newControls(),
	}
}

// Run serves until the context ends.
func (d *Daemon) Run(ctx context.Context) error {
	listener, endpoint, err := listen(d.dir)
	if err != nil {
		return err
	}
	d.endpoint = endpoint
	server := &http.Server{Handler: d.handler()}

	go d.deviceLoop(ctx)
	go d.lightsLoop(ctx)
	go d.reapLoop(ctx)
	go d.desktop.Follow(ctx, d.onFocus)
	go func() {
		<-ctx.Done()
		server.Close()
	}()
	d.log.Println("daemon", d.version, "listening on", endpoint.Socket+endpoint.URL)
	if err := server.Serve(listener); err != http.ErrServerClosed {
		return err
	}
	return nil
}

// -- the API ------------------------------------------------------------------

func (d *Daemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /attach", d.handleAttach)
	mux.HandleFunc("POST /sessions/{id}/state", d.handleState)
	mux.HandleFunc("POST /sessions/{id}/ask", d.handleAsk)
	mux.HandleFunc("GET /status", d.handleStatus)
	if d.endpoint.Token == "" {
		return mux
	}
	want := []byte("Bearer " + d.endpoint.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (d *Daemon) handleAttach(w http.ResponseWriter, r *http.Request) {
	var hello Hello
	if err := json.NewDecoder(r.Body).Decode(&hello); err != nil || hello.PID <= 0 {
		reply(w, http.StatusBadRequest, map[string]string{"type": "error", "message": "bad hello"})
		return
	}
	switch {
	case hello.Protocol != Protocol:
		reply(w, http.StatusConflict, map[string]string{"type": "error",
			"message": "the codex-micro daemon and this session's plugin differ; restart Claude Code"})
		return
	case newer(hello.Version, d.version):
		// A newer plugin: make way for its daemon.
		d.log.Println("newer client", hello.Version, "- exiting")
		reply(w, http.StatusServiceUnavailable, map[string]any{"type": "restart"})
		time.AfterFunc(200*time.Millisecond, d.exit)
		return
	case d.handOff(hello.App):
		d.log.Println("blocked from the pad under", d.origin.App, "- making way for", hello.App)
		reply(w, http.StatusServiceUnavailable, map[string]any{"type": "restart", "blocked": d.blocked()})
		time.AfterFunc(200*time.Millisecond, d.exit)
		return
	}

	s := d.attach(hello)
	defer d.detach(s)
	w.Header().Set("Content-Type", "application/x-ndjson")
	flusher, _ := w.(http.Flusher)
	for {
		select {
		case msg := <-s.out:
			if _, err := w.Write(msg); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		case <-s.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (d *Daemon) handleState(w http.ResponseWriter, r *http.Request) {
	var body struct {
		State  string `json:"state"`
		Claude string `json:"claude_session"`
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || looks[body.State] == (look{}) {
		http.Error(w, "unknown state", http.StatusBadRequest)
		return
	}
	d.mu.Lock()
	s := d.sessions[r.PathValue("id")]
	if s != nil {
		if body.Claude != "" {
			s.claude = body.Claude
		}
		d.setState(s, body.State, false)
	}
	d.mu.Unlock()
	if s == nil {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d *Daemon) handleAsk(w http.ResponseWriter, r *http.Request) {
	ask := make(chan string, 1)
	d.mu.Lock()
	s := d.sessions[r.PathValue("id")]
	if s != nil {
		d.dropAsk(s)
		s.ask = ask
	}
	d.mu.Unlock()
	if s == nil {
		http.Error(w, "no such session", http.StatusNotFound)
		return
	}

	var answer *string
	select {
	case a, ok := <-ask:
		if ok {
			answer = &a
		}
	case <-s.done:
	case <-r.Context().Done():
	}
	d.mu.Lock()
	if s.ask == ask {
		s.ask = nil
	}
	d.mu.Unlock()
	reply(w, http.StatusOK, map[string]*string{"answer": answer})
}

// dropAsk ends a session's wait on the pad unanswered. The caller holds d.mu.
func (d *Daemon) dropAsk(s *Session) {
	if s.ask != nil {
		close(s.ask)
		s.ask = nil
	}
}

// Status is what GET /status answers.
type Status struct {
	Version  string          `json:"version"`
	Device   DeviceInfo      `json:"device"`
	Desktop  string          `json:"desktop"`
	Sessions []SessionStatus `json:"sessions"`
}

// SessionStatus describes one session.
type SessionStatus struct {
	PID     int    `json:"pid"`
	CWD     string `json:"cwd"`
	Key     *int   `json:"key"`
	State   string `json:"state"`
	Window  string `json:"window,omitempty"`
	Focused bool   `json:"focused"`
}

func (d *Daemon) handleStatus(w http.ResponseWriter, _ *http.Request) {
	d.mu.Lock()
	st := Status{Version: d.version, Device: d.info, Desktop: d.desktop.Name(), Sessions: []SessionStatus{}}
	for _, s := range d.sessions {
		st.Sessions = append(st.Sessions, SessionStatus{
			PID: s.pid, CWD: s.cwd, Key: keyOf(s), State: s.state, Window: s.window, Focused: s == d.focused,
		})
	}
	d.mu.Unlock()
	reply(w, http.StatusOK, st)
}

// handOff reports whether to make way for a daemon started from app: this
// one is blocked from the pad, and app is not known to be.
func (d *Daemon) handOff(app string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.mayHandTo(app)
}

// mayHandTo is handOff for a caller holding d.mu.
func (d *Daemon) mayHandTo(app string) bool {
	return d.notAllowed && app != "" && app != d.origin.App && !slices.Contains(d.origin.Blocked, app)
}

// blocked lists the apps known to be kept from the pad, this one's included.
func (d *Daemon) blocked() []string {
	return slices.Concat(d.origin.Blocked, []string{d.origin.App})
}

// makeWay has a session under another app start a daemon in this one's
// place, now that this one is found blocked. The caller holds d.mu.
func (d *Daemon) makeWay() {
	for _, s := range d.sessions {
		if d.mayHandTo(s.app) {
			d.log.Println("blocked from the pad under", d.origin.App, "- making way for", s.app)
			d.send(s, map[string]any{"type": "restart", "blocked": d.blocked()})
			time.AfterFunc(200*time.Millisecond, d.exit)
			return
		}
	}
}

// -- sessions -----------------------------------------------------------------

func (d *Daemon) attach(hello Hello) *Session {
	env := map[string]string{}
	for _, k := range terminals.EnvKeys {
		if v, ok := hello.Env[k]; ok {
			env[k] = v
		}
	}
	s := &Session{
		id: newID(), claude: hello.Claude, app: hello.App, pid: hello.PID, cwd: hello.CWD, env: env, state: Idle, slot: -1, since: time.Now(),
		out: make(chan []byte, 64), done: make(chan struct{}), located: make(chan struct{}),
	}

	d.mu.Lock()
	var old *Session
	for _, o := range d.sessions {
		if o.pid == s.pid {
			old = o
		}
	}
	if old != nil {
		// The same session, back: it keeps its key and state.
		s.slot, s.state = old.slot, old.state
		d.remove(old)
	} else {
		s.slot = d.freeSlot(hello.Slot)
	}
	d.sessions[s.id] = s
	d.send(s, struct {
		Type    string `json:"type"`
		Session string `json:"session"`
		Endpoint
	}{"attached", s.id, d.endpoint})
	d.changed()
	d.mu.Unlock()

	d.log.Println("attach", s.pid, "key", keyName(s), s.cwd)
	go d.windowOf(s)
	return s
}

func (d *Daemon) detach(s *Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.sessions[s.id] == s {
		d.remove(s)
		d.log.Println("detach", s.pid)
		d.changed()
	}
}

// remove forgets a session. The caller holds d.mu.
func (d *Daemon) remove(s *Session) {
	delete(d.sessions, s.id)
	s.close()
	if d.focused == s {
		d.focused = nil
	}
	if d.last == s {
		d.last = nil
	}
}

// freeSlot is the key a session asks for, when free, else the first free key,
// else -1. The caller holds d.mu.
func (d *Daemon) freeSlot(wanted *int) int {
	used := map[int]bool{}
	for _, s := range d.sessions {
		used[s.slot] = true
	}
	if wanted != nil && *wanted >= 0 && *wanted < slots && !used[*wanted] {
		return *wanted
	}
	for i := range slots {
		if !used[i] {
			return i
		}
	}
	return -1
}

// send queues a message for a session. The caller holds d.mu.
func (d *Daemon) send(s *Session, msg any) {
	line, err := json.Marshal(msg)
	if err != nil {
		return
	}
	select {
	case s.out <- append(line, '\n'):
	default:
		d.log.Println("session", s.pid, "is not reading; dropped", string(line))
	}
}

// changed tells every session the news and has the lights sent. The caller
// holds d.mu.
func (d *Daemon) changed() {
	d.relight()
	for _, s := range d.sessions {
		waiting := 0
		for _, o := range d.sessions {
			if o != s && o.state == Awaiting {
				waiting++
			}
		}
		d.send(s, map[string]any{"type": "status", "slot": slotOf(s), "device": d.info, "waiting": waiting})
	}
}

// setState moves a session to a state; tell says the session did not move
// itself and should hear of it. The caller holds d.mu.
func (d *Daemon) setState(s *Session, state string, tell bool) {
	if s.state == state {
		return
	}
	if s.state == Awaiting {
		// Answered some other way, or gone.
		d.dropAsk(s)
	} else if state == Working {
		s.started = time.Now()
	}
	s.state, s.since = state, time.Now()
	if tell {
		d.send(s, map[string]string{"type": "state", "state": state})
	}
	d.changed()
}

// live reports whether a session is still attached. The caller holds d.mu.
func (d *Daemon) live(s *Session) bool {
	return s != nil && d.sessions[s.id] == s
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func slotOf(s *Session) *int {
	if s.slot < 0 {
		return nil
	}
	return &s.slot
}

func keyOf(s *Session) *int {
	if s.slot < 0 {
		return nil
	}
	key := s.slot + 1
	return &key
}

func keyName(s *Session) string {
	if s.slot < 0 {
		return "none"
	}
	return strconv.Itoa(s.slot + 1)
}

// newer reports whether version a is newer than b, both like "1.2.3".
func newer(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
