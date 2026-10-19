// Package config holds where this program keeps its files, and what each
// control on the pad does.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// RuntimeDir holds the daemon's socket or endpoint file, and its lock.
func RuntimeDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "codex-micro")
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "codex-micro")
	}
	return filepath.Join(os.TempDir(), fmt.Sprintf("codex-micro-%d", os.Getuid()))
}

// MakePrivateDir makes dir, or checks that it is this user's alone: anyone
// who could place a socket in it could pose as the daemon.
func MakePrivateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return CheckPrivateDir(dir)
}

// Socket is where the daemon listens, on macOS and Linux.
func Socket() string { return filepath.Join(RuntimeDir(), "daemon.sock") }

// EndpointFile is where the daemon tells its address and secret, on Windows.
func EndpointFile() string { return filepath.Join(RuntimeDir(), "daemon.json") }

// LogFile is where the daemon writes what it does.
func LogFile() string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(RuntimeDir(), "daemon.log")
	case "darwin":
		return filepath.Join(home, "Library", "Logs", "codex-micro", "daemon.log")
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "codex-micro", "daemon.log")
}

// File is the user's config.toml.
func File() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "codex-micro", "config.toml")
}
