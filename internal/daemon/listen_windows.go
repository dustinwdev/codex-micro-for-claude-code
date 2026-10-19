package daemon

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
)

// lock is held for the life of the process, so only one daemon runs.
var lock *os.File

// listen takes the daemon's lock and listens on a loopback port. Any local
// user can reach a port, so each request must carry the secret written,
// with the address, to an endpoint file in dir that only this user can read.
func listen(dir string) (net.Listener, Endpoint, error) {
	if err := config.MakePrivateDir(dir); err != nil {
		return nil, Endpoint{}, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, Endpoint{}, err
	}
	const exclusive = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	// A daemon making way for a newer one takes a moment to exit.
	for attempt := 0; windows.LockFileEx(windows.Handle(f.Fd()), exclusive, 0, 1, 0, &windows.Overlapped{}) != nil; attempt++ {
		if attempt == 30 {
			f.Close()
			return nil, Endpoint{}, errors.New("another daemon is running")
		}
		time.Sleep(100 * time.Millisecond)
	}
	lock = f

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, Endpoint{}, err
	}
	endpoint := Endpoint{URL: "http://" + l.Addr().String(), Token: newID() + newID()}
	data, _ := json.Marshal(endpoint)
	if err := os.WriteFile(config.EndpointFile(), data, 0o600); err != nil {
		l.Close()
		return nil, Endpoint{}, err
	}
	return l, endpoint, nil
}

// detach starts a command apart from this one's console and job, so it
// outlives the daemon.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
}
