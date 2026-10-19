package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

// The Claude app's Code tab keeps a record of each session it runs, naming
// it by an id of its own beside the Claude Code session's.
var appSessionID = regexp.MustCompile(`^local_[A-Za-z0-9-]{1,64}$`)

// claudeAppLink is the link that opens a Claude Code session in the Claude
// app, or "" when the app doesn't run it.
func claudeAppLink(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Library", "Application Support", "Claude")
	if runtime.GOOS == "windows" {
		dir = filepath.Join(os.Getenv("APPDATA"), "Claude")
	}
	records, _ := filepath.Glob(filepath.Join(dir, "claude-code-sessions", "*", "*", "local_*.json"))
	for _, path := range records {
		var r struct {
			ID     string `json:"sessionId"`
			Claude string `json:"cliSessionId"`
		}
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &r) == nil && r.Claude == sessionID && appSessionID.MatchString(r.ID) {
			return "claude://code/continue?session=" + r.ID
		}
	}
	return ""
}
