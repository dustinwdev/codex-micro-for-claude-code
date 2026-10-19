// Package proc finds out about processes: their parents, their terminal
// device, and when they exit.
package proc

import "strings"

// App is the outermost app bundle a process runs under, such as
// /Applications/Ghostty.app, or "". On macOS, the app whose permissions it
// has.
func App(pid int) string {
	app := ""
	for _, p := range Ancestors(pid) {
		if path, _, ok := strings.Cut(Command(p), ".app/Contents/MacOS/"); ok {
			app = path + ".app"
		}
	}
	return app
}

// Ancestors lists pid and each of its parents, nearest first.
func Ancestors(pid int) []int {
	var out []int
	seen := map[int]bool{}
	for pid > 1 && !seen[pid] {
		seen[pid] = true
		out = append(out, pid)
		pid = parent(pid)
	}
	return out
}
