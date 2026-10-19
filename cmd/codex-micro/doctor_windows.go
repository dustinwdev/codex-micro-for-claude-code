package main

import (
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/config"
	"github.com/dustinwdev/codex-micro-for-claude-code/internal/keys"
)

func accessFix() string { return "" }

func desktopChecks(c *checker, cfg config.Config) {
	c.check(true, "Windows: agent keys bring the terminal forward; approve and deny answer prompts", "")
	for _, held := range holds(cfg) {
		chord := keys.Parse(held)
		if chord.Key == "fn" || chord.Has("fn") {
			c.check(false, "holding "+held+" for dictation",
				"Windows can't press fn. Use your dictation app's other shortcut,\n"+
					"such as ctrl+win for Wispr Flow.")
		}
	}
}
