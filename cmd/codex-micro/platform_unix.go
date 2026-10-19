//go:build !windows

package main

import (
	"context"
	"net"
	"net/http"
	"os/exec"
	"syscall"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
)

const noctty = syscall.O_NOCTTY

// dial connects to the daemon's socket, once the directory holding it is
// known to be this user's alone.
func dial(ctx context.Context) (net.Conn, error) {
	if err := config.MakePrivateDir(config.RuntimeDir()); err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", config.Socket())
}

// authorized sends requests as they are: the socket's permissions are the
// daemon's guard.
type authorized struct{ http.RoundTripper }

// startDetached starts a command in a session of its own, so it outlives
// this one.
func startDetached(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
