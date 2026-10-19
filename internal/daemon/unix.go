//go:build !windows

package daemon

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
)

// lock is held for the life of the process, so only one daemon runs.
var lock *os.File

// listen takes the daemon's lock and listens on a socket in dir.
func listen(dir string) (net.Listener, Endpoint, error) {
	socket := filepath.Join(dir, "daemon.sock")
	if err := config.MakePrivateDir(dir); err != nil {
		return nil, Endpoint{}, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, Endpoint{}, err
	}
	// A daemon making way for a newer one takes a moment to exit.
	for attempt := 0; unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil; attempt++ {
		if attempt == 30 {
			f.Close()
			return nil, Endpoint{}, errors.New("another daemon is running")
		}
		time.Sleep(100 * time.Millisecond)
	}
	lock = f
	os.Remove(socket)
	l, err := net.Listen("unix", socket)
	if err != nil {
		return nil, Endpoint{}, err
	}
	return l, Endpoint{Socket: socket}, os.Chmod(socket, 0o600)
}

// detach starts a command in a session of its own, so it outlives the daemon.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
