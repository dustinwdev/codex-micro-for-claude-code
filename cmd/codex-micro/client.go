package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/proc"
)

const logLimit = 1 << 20

// daemonClient talks HTTP to the daemon, starting the daemon when nothing
// answers.
var daemonClient = &http.Client{Transport: authorized{&http.Transport{
	DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		conn, err := dial(ctx)
		if err == nil {
			return conn, nil
		}
		if err := startDaemon(nil); err != nil {
			return nil, err
		}
		for range 40 {
			time.Sleep(100 * time.Millisecond)
			if conn, err := dial(ctx); err == nil {
				return conn, nil
			}
		}
		return nil, fmt.Errorf("the daemon did not start; see %s", config.LogFile())
	},
}}}

// startDaemon starts the daemon through a short-lived `start`, so that the
// daemon belongs to no session's process tree: Claude Code ends a session's
// whole tree when it exits. blocked lists apps known to be kept from the pad.
func startDaemon(blocked []string) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "start")
	cmd.Env = append(os.Environ(), "CODEX_MICRO_BLOCKED="+strings.Join(blocked, "\n"))
	return cmd.Run()
}

// spawnDaemon starts the daemon in the background, logging to the log file.
func spawnDaemon() error {
	path := config.LogFile()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil && info.Size() > logLimit {
		os.Rename(path, path+".1")
	}
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "daemon")
	cmd.Env = append(os.Environ(), "CODEX_MICRO_APP="+proc.App(os.Getpid()))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	return startDetached(cmd)
}

// get asks the daemon something.
func get(path string, v any) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://codex-micro"+path, nil)
	resp, err := daemonClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New(resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// post sends the daemon a JSON body, and returns the response unread.
func post(ctx context.Context, path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "http://codex-micro"+path, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	return daemonClient.Do(req)
}

// errorOf reads the message from an error response.
func errorOf(resp *http.Response) string {
	var body struct {
		Message string `json:"message"`
	}
	data, _ := io.ReadAll(resp.Body)
	if json.Unmarshal(data, &body) == nil && body.Message != "" {
		return body.Message
	}
	return resp.Status
}
