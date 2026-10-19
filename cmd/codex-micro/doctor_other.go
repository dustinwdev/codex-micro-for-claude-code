//go:build !linux && !darwin && !windows

package main

import "github.com/dustinwdev/codex-micro-for-claude-code/internal/config"

func accessFix() string { return "" }

func desktopChecks(c *checker, _ config.Config) {
	c.check(false, "this platform is not supported yet", "")
}
