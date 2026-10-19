package main

import (
	"os"
	"strings"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/desktop"
)

func accessFix() string { return "" }

func desktopChecks(c *checker, cfg config.Config) {
	app := desktop.HostApp(os.Getppid())
	switch app {
	case "Ghostty":
		c.check(true, "Ghostty: agent keys switch terminals; approve presses Enter, deny presses Escape", "")
	case "iTerm2":
		c.check(true, "iTerm2: agent keys switch sessions; approve and deny answer prompts", "")
	case "Terminal":
		c.check(true, "Terminal: agent keys switch tabs; approve and deny need Ghostty, iTerm2 or tmux", "")
	default:
		c.check(false, "terminal app not recognized",
			"Agent keys, approve and deny work in Ghostty, iTerm2, Terminal, or inside tmux.")
	}
	// The Claude app runs Code tab sessions on a Claude Code of its own, which
	// runs plugin hooks only when asked to.
	if _, err := os.Stat("/Applications/Claude.app"); err == nil && os.Getenv("CLAUDE_CODE_ENABLE_FUNCTION_HOOKS") == "" {
		c.note("Claude app: to use the pad in its Code tab, add this to ~/.claude/settings.json")
		c.note(`  "env": { "CLAUDE_CODE_ENABLE_FUNCTION_HOOKS": "1" }`)
	}
	if held := holds(cfg); len(held) > 0 {
		if app == "" {
			app = "your terminal app"
		}
		c.check(desktop.Trusted(), "holding "+strings.Join(held, ", ")+" for dictation",
			"Allow "+app+" under System Settings > Privacy & Security > Accessibility,\n"+
				"then quit and reopen it.")
	}
}
