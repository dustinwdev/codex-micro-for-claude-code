package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/desktop"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/keys"
)

// fakeDesktop finds each session in a window named for its pid, and records
// what is done to windows.
type fakeDesktop struct {
	desktop.None
	mu   sync.Mutex
	done []string
}

func (f *fakeDesktop) Name() string                    { return "fake" }
func (f *fakeDesktop) Locate(pid int, _ string) string { return "w" + strconv.Itoa(pid) }
func (f *fakeDesktop) record(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.done = append(f.done, s)
	return true
}
func (f *fakeDesktop) Focus(w string) bool               { return f.record("focus " + w) }
func (f *fakeDesktop) Approve(w string) bool             { return f.record("approve " + w) }
func (f *fakeDesktop) Press(w string, c keys.Chord) bool { return f.record("press " + c.Key + " " + w) }
func (f *fakeDesktop) Hold(c keys.Chord, down bool) error {
	f.record("hold " + c.Key + " " + map[bool]string{true: "down", false: "up"}[down])
	return nil
}

// await waits for what the desktop is asked to do.
func (f *fakeDesktop) await(t *testing.T, want ...string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		f.mu.Lock()
		got := append([]string(nil), f.done...)
		f.mu.Unlock()
		if equal(got, want) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("desktop did %q, want %q", got, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type harness struct {
	t       *testing.T
	daemon  *Daemon
	desktop *fakeDesktop
	server  *httptest.Server
}

func newHarness(t *testing.T, configText string) *harness {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if configText != "" {
		os.MkdirAll(filepath.Join(dir, "codex-micro"), 0o755)
		os.WriteFile(filepath.Join(dir, "codex-micro", "config.toml"), []byte(configText), 0o644)
	}
	fd := &fakeDesktop{}
	d := New("1.0.0", t.TempDir(), Origin{}, fd, log.New(io.Discard, "", 0))
	server := httptest.NewServer(d.handler())
	t.Cleanup(server.Close)
	return &harness{t, d, fd, server}
}

// client is an attached session, as its attach process sees it.
type client struct {
	t     *testing.T
	id    string
	lines *bufio.Scanner
	stop  context.CancelFunc
}

func (h *harness) attach(pid int, slot *int) *client {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	h.t.Cleanup(cancel)
	body, _ := json.Marshal(Hello{PID: pid, CWD: "/src", Slot: slot, Version: "1.0.0", Protocol: Protocol})
	req, _ := http.NewRequestWithContext(ctx, "POST", h.server.URL+"/attach", bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		h.t.Fatalf("attach: %v %v", err, resp)
	}
	c := &client{t: h.t, lines: bufio.NewScanner(resp.Body), stop: cancel}
	c.id = c.next("attached")["session"].(string)
	return c
}

// next reads messages until one of the given type.
func (c *client) next(kind string) map[string]any {
	c.t.Helper()
	for c.lines.Scan() {
		var m map[string]any
		json.Unmarshal(c.lines.Bytes(), &m)
		if m["type"] == kind {
			return m
		}
	}
	c.t.Fatalf("stream ended before a %q message", kind)
	return nil
}

func (h *harness) setState(c *client, state string) {
	h.t.Helper()
	resp, err := http.Post(h.server.URL+"/sessions/"+c.id+"/state", "application/json",
		bytes.NewReader([]byte(`{"state":"`+state+`"}`)))
	if err != nil || resp.StatusCode != http.StatusNoContent {
		h.t.Fatalf("set state: %v %v", err, resp)
	}
}

func (h *harness) waitFor(cond func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			h.t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSessionsGetKeysInOrderAndFreeThemOnDetach(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	if slot := a.next("status")["slot"]; slot != 0.0 {
		t.Fatalf("first session got slot %v", slot)
	}
	b := h.attach(102, nil)
	if slot := b.next("status")["slot"]; slot != 1.0 {
		t.Fatalf("second session got slot %v", slot)
	}
	a.stop()
	h.waitFor(func() bool {
		h.daemon.mu.Lock()
		defer h.daemon.mu.Unlock()
		return len(h.daemon.sessions) == 1
	})
	c := h.attach(103, nil)
	if slot := c.next("status")["slot"]; slot != 0.0 {
		t.Fatalf("freed key not reused: slot %v", slot)
	}
}

func TestASessionKeepsItsKeyAcrossRestarts(t *testing.T) {
	h := newHarness(t, "")
	four := 3
	a := h.attach(101, &four)
	if slot := a.next("status")["slot"]; slot != 3.0 {
		t.Fatalf("asked for slot 3, got %v", slot)
	}
}

func TestOthersHearHowManyWait(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	b := h.attach(102, nil)
	h.setState(a, Awaiting)
	for {
		if m := b.next("status"); m["waiting"] == 1.0 {
			break
		}
	}
}

func TestApproveGoesToTheSessionWaiting(t *testing.T) {
	h := newHarness(t, "")
	h.attach(101, nil)
	b := h.attach(102, nil)
	h.setState(b, Awaiting)
	h.daemon.onKey("ACT07", 1)
	h.desktop.await(t, "approve w102")
}

func TestDenyWithNothingWaitingInterrupts(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	h.setState(a, Working)
	h.daemon.onKey("ACT08", 1)
	if m := a.next("action"); m["action"] != "interrupt" {
		t.Fatalf("got %v", m)
	}
}

func TestMicHoldsAKeyWhileEitherSwitchIsDown(t *testing.T) {
	h := newHarness(t, "[keys]\nmic = { hold = \"fn\" }\n")
	h.daemon.onKey("ACT10", 1)
	h.daemon.onKey("ACT11", 1)
	h.daemon.onKey("ACT10", 0)
	h.daemon.onKey("ACT11", 0)
	h.desktop.await(t, "hold fn down", "hold fn up")
}

func TestStickActsOncePerPush(t *testing.T) {
	h := newHarness(t, `
[stick]
up = { text = "up" }
down = { text = "down" }
left = { text = "left" }
right = { text = "right" }
`)
	a := h.attach(101, nil)
	for _, push := range []struct {
		angle float64
		want  string
	}{{0, "right"}, {0.25, "down"}, {0.5, "left"}, {0.75, "up"}, {0.98, "right"}} {
		h.daemon.onStick(push.angle, 0.9)
		h.daemon.onStick(push.angle, 1.0) // still pushed: no second action
		h.daemon.onStick(push.angle, 0.1)
		if m := a.next("action"); m["text"] != push.want {
			t.Fatalf("angle %v: got %v, want %s", push.angle, m["text"], push.want)
		}
	}
}

func TestNewerVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want bool
	}{{"0.10.0", "0.9.1", true}, {"1.0", "1.0.0", false}, {"0.7.0", "0.7.1", false}} {
		if got := newer(tt.a, tt.b); got != tt.want {
			t.Errorf("newer(%s, %s) = %v", tt.a, tt.b, got)
		}
	}
}

// ask waits, as the plugin's permission hook does, for the pad's answer.
func (h *harness) ask(c *client) <-chan any {
	answer := make(chan any, 1)
	go func() {
		resp, err := http.Post(h.server.URL+"/sessions/"+c.id+"/ask", "application/json", nil)
		if err != nil {
			answer <- err
			return
		}
		var body map[string]any
		json.NewDecoder(resp.Body).Decode(&body)
		answer <- body["answer"]
	}()
	return answer
}

// waitForAsk waits until the session's ask is registered.
func (h *harness) waitForAsk(c *client) {
	h.waitFor(func() bool {
		h.daemon.mu.Lock()
		defer h.daemon.mu.Unlock()
		return h.daemon.sessions[c.id].ask != nil
	})
}

func TestThePadAnswersAPromptThePluginWaitsOn(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	h.setState(a, Awaiting)
	answer := h.ask(a)
	h.waitForAsk(a)
	h.daemon.onKey("ACT08", 1)
	if got := <-answer; got != "deny" {
		t.Fatalf("answer = %v, want deny", got)
	}
	h.desktop.await(t) // no keys typed
}

func TestAPromptAnsweredElsewhereStopsWaiting(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	h.setState(a, Awaiting)
	answer := h.ask(a)
	h.waitForAsk(a)
	h.setState(a, Working)
	if got := <-answer; got != nil {
		t.Fatalf("answer = %v, want none", got)
	}
	// With nothing waiting on the pad, approve falls back to keys.
	h.setState(a, Awaiting)
	h.daemon.onKey("ACT07", 1)
	h.desktop.await(t, "approve w101")
}

func TestANewerPromptReplacesTheOlder(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	h.setState(a, Awaiting)
	first := h.ask(a)
	h.waitForAsk(a)
	second := h.ask(a)
	if got := <-first; got != nil {
		t.Fatalf("first answer = %v, want none", got)
	}
	h.waitForAsk(a)
	h.daemon.onKey("ACT07", 1)
	if got := <-second; got != "allow" {
		t.Fatalf("second answer = %v, want allow", got)
	}
}

func TestABlockedDaemonMakesWayForAnotherApp(t *testing.T) {
	h := newHarness(t, "")
	exited := make(chan struct{}, 1)
	h.daemon.exit = func() { exited <- struct{}{} }
	h.daemon.origin = Origin{App: "/Applications/Claude.app", Blocked: []string{"/Applications/Other.app"}}
	h.daemon.notAllowed = true

	post := func(app string) *http.Response {
		body, _ := json.Marshal(Hello{PID: 101, App: app, Version: "1.0.0", Protocol: Protocol})
		resp, err := http.Post(h.server.URL+"/attach", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// Apps already blocked are not made way for.
	for _, app := range []string{"/Applications/Claude.app", "/Applications/Other.app"} {
		resp := post(app)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, want the session attached", app, resp.StatusCode)
		}
	}
	resp := post("/Applications/Ghostty.app")
	var restart struct{ Blocked []string }
	json.NewDecoder(resp.Body).Decode(&restart)
	if resp.StatusCode != http.StatusServiceUnavailable || len(restart.Blocked) != 2 {
		t.Fatalf("status %d, blocked %v", resp.StatusCode, restart.Blocked)
	}
	<-exited
}

func TestABlockedDaemonHasAnAttachedSessionReplaceIt(t *testing.T) {
	h := newHarness(t, "")
	exited := make(chan struct{}, 1)
	h.daemon.exit = func() { exited <- struct{}{} }
	h.daemon.origin = Origin{App: "/Applications/Claude.app"}
	body, _ := json.Marshal(Hello{PID: 101, App: "/Applications/Ghostty.app", Version: "1.0.0", Protocol: Protocol})
	resp, err := http.Post(h.server.URL+"/attach", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	c := &client{t: t, lines: bufio.NewScanner(resp.Body)}
	c.next("attached")

	h.daemon.mu.Lock()
	h.daemon.notAllowed = true
	h.daemon.makeWay()
	h.daemon.mu.Unlock()
	if m := c.next("restart"); len(m["blocked"].([]any)) != 1 {
		t.Fatalf("restart = %v", m)
	}
	<-exited
}

func TestHoldingTheCodexKeyAssignsTheCurrentSessionToAnAgentKey(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil) // key 1
	b := h.attach(102, nil) // key 2
	h.setState(a, Working)  // the session you are in

	h.daemon.onKey("ACT12", 1)
	h.daemon.onKey("AG01", 1)
	h.daemon.onKey("AG01", 0)
	h.daemon.onKey("ACT12", 0)

	for {
		if m := a.next("status"); m["slot"] == 1.0 {
			break
		}
	}
	for {
		if m := b.next("status"); m["slot"] == 0.0 {
			break
		}
	}
	h.daemon.mu.Lock()
	defer h.daemon.mu.Unlock()
	for _, s := range h.daemon.sessions {
		for len(s.out) > 0 {
			var m map[string]any
			json.Unmarshal(<-s.out, &m)
			if m["type"] == "action" {
				t.Errorf("the Codex key acted after assigning: %v", m)
			}
		}
	}
}

func TestTheCodexKeyAloneSubmitsOnRelease(t *testing.T) {
	h := newHarness(t, "")
	a := h.attach(101, nil)
	h.daemon.onKey("ACT12", 1)
	h.daemon.onKey("ACT12", 0)
	if m := a.next("action"); m["action"] != "submit" {
		t.Fatalf("got %v", m)
	}
}
