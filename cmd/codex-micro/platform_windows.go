package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/daemon"
)

const noctty = 0

// endpoint reads where the daemon listens, and its secret.
func endpoint() (daemon.Endpoint, error) {
	var e daemon.Endpoint
	data, err := os.ReadFile(config.EndpointFile())
	if err == nil {
		err = json.Unmarshal(data, &e)
	}
	return e, err
}

func dial(ctx context.Context) (net.Conn, error) {
	e, err := endpoint()
	if err != nil {
		return nil, err
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", strings.TrimPrefix(e.URL, "http://"))
}

// authorized presents the daemon's secret with each request.
type authorized struct{ http.RoundTripper }

func (a authorized) RoundTrip(r *http.Request) (*http.Response, error) {
	e, _ := endpoint()
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+e.Token)
	return a.RoundTripper.RoundTrip(r)
}

// startDetached starts a command with no console, outside the job Claude
// Code runs its children in where the job allows, so it outlives them.
func startDetached(cmd *exec.Cmd) error {
	flags := uint32(windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP)
	path, args, dir, env, stdout, stderr := cmd.Path, cmd.Args, cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags | windows.CREATE_BREAKAWAY_FROM_JOB}
	if err := cmd.Start(); err != nil {
		// The job forbids leaving it: a Cmd starts once, so make another.
		cmd = exec.Command(path, args[1:]...)
		cmd.Dir, cmd.Env, cmd.Stdout, cmd.Stderr = dir, env, stdout, stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: flags}
		if err := cmd.Start(); err != nil {
			return err
		}
	}
	return cmd.Process.Release()
}
