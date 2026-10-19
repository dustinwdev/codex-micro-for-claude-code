//go:build !windows

package proc

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// Alive reports whether a process is running.
func Alive(pid int) bool {
	return !errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}

// Command is the path of the program a process runs, or "".
func Command(pid int) string {
	return ps(pid, "comm=")
}

// TTY is the terminal device a process runs on, such as /dev/ttys003, or "".
func TTY(pid int) string {
	tty := ps(pid, "tty=")
	switch {
	case tty == "" || tty == "?" || tty == "??":
		return ""
	case strings.HasPrefix(tty, "/dev/"):
		return tty
	}
	return "/dev/" + tty
}

func ps(pid int, field string) string {
	out, err := exec.Command("ps", "-o", field, "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
