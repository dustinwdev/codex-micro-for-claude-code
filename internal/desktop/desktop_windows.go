package desktop

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/windows"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/keys"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/win32"
)

// Windows types into a session's console directly, so it works in any
// terminal without focusing it, and brings windows forward through user32.
// Consoles are "console:<pid>"; windows are window handles in hex.
type Windows struct {
	None
}

// Detect returns the desktop this session runs on.
func Detect(string) Desktop {
	return &Windows{}
}

func (*Windows) Name() string { return "Windows" }

// A process has one console at a time: one attach at a time.
var consoleMu sync.Mutex

// withConsole runs fn attached to the console pid uses.
func withConsole(pid int, fn func() error) error {
	consoleMu.Lock()
	defer consoleMu.Unlock()
	win32.FreeConsole()
	if err := win32.AttachConsole(pid); err != nil {
		return err
	}
	defer win32.FreeConsole()
	return fn()
}

func (*Windows) Console(pid int) string { return "console:" + strconv.Itoa(pid) }

// Locate finds the window showing pid's console: under Windows Terminal,
// the terminal's window, which owns the console's own hidden one.
func (*Windows) Locate(pid int, _ string) string {
	var window windows.HWND
	withConsole(pid, func() error {
		window = win32.ConsoleWindow()
		if owner := win32.Owner(window); owner != 0 {
			window = owner
		}
		return nil
	})
	if window == 0 {
		return ""
	}
	return fmt.Sprintf("%x", uintptr(window))
}

func handle(window string) windows.HWND {
	h, _ := strconv.ParseUint(window, 16, 64)
	return windows.HWND(h)
}

// Focus brings a window forward. Windows lets only the app in front take
// focus, unless the last input came from the caller: a tap of Alt does.
func (*Windows) Focus(window string) bool {
	h := handle(window)
	if h == 0 {
		return false
	}
	win32.Restore(h)
	alt := uint16(0x12) // VK_MENU
	win32.SendInput([]win32.KeyInput{
		{Type: 1, VirtualKey: alt},
		{Type: 1, VirtualKey: alt, Flags: win32.KeyUp},
	})
	return win32.SetForegroundWindow(h)
}

const (
	keyEvent     = 1    // KEY_EVENT
	shiftPressed = 0x10 // SHIFT_PRESSED
	ctrlPressed  = 0x08 // LEFT_CTRL_PRESSED
	altPressed   = 0x02 // LEFT_ALT_PRESSED
)

// consoleKeys are the named keys a console takes: virtual key and character.
var consoleKeys = map[string][2]uint16{
	"escape": {0x1B, 0x1B}, "enter": {0x0D, '\r'}, "tab": {0x09, '\t'},
	"space": {0x20, ' '}, "backspace": {0x08, 0x08},
}

// Press types a chord into a session's console.
func (*Windows) Press(window string, c keys.Chord) bool {
	pidText, ok := strings.CutPrefix(window, "console:")
	pid, err := strconv.Atoi(pidText)
	if !ok || err != nil {
		return false
	}
	down, ok := consoleKey(c)
	if !ok {
		return false
	}
	up := down
	up.KeyDown = 0
	err = withConsole(pid, func() error {
		input, err := windows.CreateFile(windows.StringToUTF16Ptr("CONIN$"),
			windows.GENERIC_READ|windows.GENERIC_WRITE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			return err
		}
		defer windows.CloseHandle(input)
		return win32.WriteConsoleInput(input, []win32.KeyEvent{down, up})
	})
	return err == nil
}

func consoleKey(c keys.Chord) (win32.KeyEvent, bool) {
	e := win32.KeyEvent{EventType: keyEvent, KeyDown: 1, RepeatCount: 1}
	if named, ok := consoleKeys[c.Key]; ok {
		e.VirtualKey, e.Char = named[0], named[1]
	} else if len(c.Key) == 1 && ('a' <= c.Key[0] && c.Key[0] <= 'z' || '0' <= c.Key[0] && c.Key[0] <= '9') {
		e.VirtualKey, e.Char = uint16(strings.ToUpper(c.Key)[0]), uint16(c.Key[0])
	} else {
		return e, false
	}
	for _, m := range c.Mods {
		switch m {
		case "shift":
			e.ControlState |= shiftPressed
		case "ctrl":
			e.ControlState |= ctrlPressed
			if 'a' <= c.Key[0] && c.Key[0] <= 'z' && len(c.Key) == 1 {
				e.Char = uint16(c.Key[0] - 'a' + 1)
			}
		case "alt":
			e.ControlState |= altPressed
		default:
			return e, false
		}
	}
	return e, true
}

func (w *Windows) Approve(window string) bool {
	return w.Press(window, keys.Chord{Key: "1"})
}

// Follow polls the window in front: cheap, and needs no message loop.
func (*Windows) Follow(ctx context.Context, onFocus func(string)) {
	var last windows.HWND
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
		if front := win32.ForegroundWindow(); front != last {
			last = front
			onFocus(fmt.Sprintf("%x", uintptr(front)))
		}
	}
}

func (*Windows) Show(sessionID string) bool {
	link := claudeAppLink(sessionID)
	return link != "" && exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", link).Run() == nil
}

// Open runs argv in a new Windows Terminal window, or a console window.
func (*Windows) Open(cwd string, argv []string, _ int) bool {
	var cmd *exec.Cmd
	if wt, err := exec.LookPath("wt.exe"); err == nil {
		cmd = exec.Command(wt, append([]string{"-w", "new", "-d", cwd}, argv...)...)
	} else {
		cmd = exec.Command("cmd.exe", append([]string{"/c", "start", "", "/D", cwd}, argv...)...)
	}
	return cmd.Start() == nil
}

// virtualKeys are the keys Hold can press, by name.
var virtualKeys = map[string]uint16{
	"ctrl": 0x11, "shift": 0x10, "alt": 0x12, "win": 0x5B, "cmd": 0x5B,
	"right-ctrl": 0xA3, "right-shift": 0xA1, "right-alt": 0xA5, "right-win": 0x5C, "right-cmd": 0x5C,
	"escape": 0x1B, "enter": 0x0D, "tab": 0x09, "space": 0x20, "backspace": 0x08,
}

func virtualKey(name string) (uint16, error) {
	if vk, ok := virtualKeys[name]; ok {
		return vk, nil
	}
	switch {
	case name == "fn":
		return 0, errors.New("Windows can't press fn: use your dictation app's other shortcut, such as ctrl+win")
	case len(name) == 1 && ('a' <= name[0] && name[0] <= 'z' || '0' <= name[0] && name[0] <= '9'):
		return uint16(strings.ToUpper(name)[0]), nil
	case len(name) >= 2 && name[0] == 'f':
		if n, err := strconv.Atoi(name[1:]); err == nil && n >= 1 && n <= 24 {
			return uint16(0x70 + n - 1), nil
		}
	}
	return 0, fmt.Errorf("no key named %q", name)
}

// Hold posts the chord's keys to the system as a keyboard would: down in
// order, or up in reverse.
func (w *Windows) Hold(c keys.Chord, down bool) error {
	names := append(append([]string(nil), c.Mods...), c.Key)
	var inputs []win32.KeyInput
	for _, name := range names {
		vk, err := virtualKey(name)
		if err != nil {
			return err
		}
		inputs = append(inputs, win32.KeyInput{Type: 1, VirtualKey: vk})
	}
	if !down {
		for i, j := 0, len(inputs)-1; i < j; i, j = i+1, j-1 {
			inputs[i], inputs[j] = inputs[j], inputs[i]
		}
		for i := range inputs {
			inputs[i].Flags = win32.KeyUp
		}
	}
	return win32.SendInput(inputs)
}
