package proc

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func parent(pid int) int {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if int(entry.ProcessID) == pid {
			return int(entry.ParentProcessID)
		}
	}
	return 0
}

// Command is the path of the program a process runs, or "".
func Command(pid int) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// TTY is the terminal device a process runs on: Windows has none.
func TTY(int) string { return "" }

// CWD is the working directory of a process: Windows does not tell.
func CWD(int) string { return "" }

// Exited is closed when pid exits.
func Exited(pid int) (<-chan struct{}, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer windows.CloseHandle(h)
		windows.WaitForSingleObject(h, windows.INFINITE)
		close(done)
	}()
	return done, nil
}

// Alive reports whether a process is running.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	const stillActive = 259
	return windows.GetExitCodeProcess(h, &code) == nil && code == stillActive
}
