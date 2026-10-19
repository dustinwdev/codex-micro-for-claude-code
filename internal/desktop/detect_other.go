//go:build !linux && !darwin && !windows

package desktop

// Detect returns the desktop this session runs on.
func Detect(string) Desktop {
	return None{}
}
