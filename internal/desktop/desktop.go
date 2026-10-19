// Package desktop finds and drives the windows sessions run in, through
// whatever the platform's desktop offers.
package desktop

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"time"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/keys"
)

// Desktop is the platform's window system. Windows are opaque names.
type Desktop interface {
	// Name is the desktop's name, or "" when none is supported.
	Name() string
	// Locate finds the window a process runs in, or "".
	Locate(pid int, cwd string) string
	// Console names a way to type into a process's console directly,
	// whichever window shows it, or "".
	Console(pid int) string
	// Titled finds the one window showing a title, or "". It tells apart
	// windows Locate can't, once a session titles its own.
	Titled(title string) string
	// Focus brings a window forward.
	Focus(window string) bool
	// Press types a key into a window, without focusing it when it can.
	Press(window string, c keys.Chord) bool
	// Approve answers a permission prompt in a window with Yes.
	Approve(window string) bool
	// Raise brings forward the app a process runs under, for windows it
	// can't name.
	Raise(pid int) bool
	// Show brings forward a Claude Code session the Claude app runs, by the
	// session's id.
	Show(sessionID string) bool
	// Open runs argv in a new window of the terminal app near runs under.
	Open(cwd string, argv []string, near int) bool
	// Hold presses or releases a chord for the whole desktop, as a keyboard
	// would: for dictation apps listening for a held key.
	Hold(c keys.Chord, down bool) error
	// Follow calls onFocus with each window that gains focus, until the
	// context ends. Windows of other apps read as "".
	Follow(ctx context.Context, onFocus func(window string))
}

// ErrUnsupported is returned for what this desktop can't do.
var ErrUnsupported = errors.New("not supported on this desktop")

// None is a desktop that supports nothing. Others embed it for what they
// don't support.
type None struct{}

func (None) Name() string                         { return "" }
func (None) Locate(int, string) string            { return "" }
func (None) Console(int) string                   { return "" }
func (None) Titled(string) string                 { return "" }
func (None) Focus(string) bool                    { return false }
func (None) Press(string, keys.Chord) bool        { return false }
func (None) Approve(string) bool                  { return false }
func (None) Raise(int) bool                       { return false }
func (None) Show(string) bool                     { return false }
func (None) Open(string, []string, int) bool      { return false }
func (None) Hold(keys.Chord, bool) error          { return ErrUnsupported }
func (None) Follow(context.Context, func(string)) {}

// output runs a program and returns what it printed, trimmed, or false when
// it failed.
func output(timeout time.Duration, argv ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output()
	return strings.TrimRight(string(out), "\n"), err == nil
}
