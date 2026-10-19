package config

import (
	"fmt"
	"os"
)

// CheckPrivateDir checks that dir is a directory. Under %LOCALAPPDATA% it is
// this user's alone.
func CheckPrivateDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	return nil
}
