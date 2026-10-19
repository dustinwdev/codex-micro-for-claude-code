//go:build windows

package win32

import (
	"testing"
	"unsafe"
)

// The structs Windows reads must match its layouts byte for byte.
func TestLayouts(t *testing.T) {
	for name, got := range map[string]uintptr{
		"HidCaps":  unsafe.Sizeof(HidCaps{}),
		"KeyEvent": unsafe.Sizeof(KeyEvent{}),
		"KeyInput": unsafe.Sizeof(KeyInput{}),
	} {
		want := map[string]uintptr{"HidCaps": 64, "KeyEvent": 20, "KeyInput": 40}[name]
		if got != want {
			t.Errorf("%s is %d bytes, want %d", name, got, want)
		}
	}
}
