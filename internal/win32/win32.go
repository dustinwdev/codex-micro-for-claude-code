//go:build windows

// Package win32 binds the Windows APIs this program uses that
// golang.org/x/sys/windows does not.
package win32

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	hid      = windows.NewLazySystemDLL("hid.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	user32   = windows.NewLazySystemDLL("user32.dll")

	hidDGetHidGuid        = hid.NewProc("HidD_GetHidGuid")
	hidDGetAttributes     = hid.NewProc("HidD_GetAttributes")
	hidDGetPreparsedData  = hid.NewProc("HidD_GetPreparsedData")
	hidDFreePreparsedData = hid.NewProc("HidD_FreePreparsedData")
	hidPGetCaps           = hid.NewProc("HidP_GetCaps")
	attachConsole         = kernel32.NewProc("AttachConsole")
	freeConsole           = kernel32.NewProc("FreeConsole")
	getConsoleWindow      = kernel32.NewProc("GetConsoleWindow")
	writeConsoleInput     = kernel32.NewProc("WriteConsoleInputW")
	getForegroundWindow   = user32.NewProc("GetForegroundWindow")
	setForegroundWindow   = user32.NewProc("SetForegroundWindow")
	getWindow             = user32.NewProc("GetWindow")
	isIconic              = user32.NewProc("IsIconic")
	showWindow            = user32.NewProc("ShowWindow")
	sendInput             = user32.NewProc("SendInput")
)

// HidGUID is the device interface class of HID devices.
func HidGUID() windows.GUID {
	var g windows.GUID
	hidDGetHidGuid.Call(uintptr(unsafe.Pointer(&g)))
	return g
}

// HidAttributes is HIDD_ATTRIBUTES.
type HidAttributes struct {
	Size      uint32
	VendorID  uint16
	ProductID uint16
	Version   uint16
}

// GetAttributes reads a HID device's vendor and product ids.
func GetAttributes(h windows.Handle) (HidAttributes, bool) {
	a := HidAttributes{Size: uint32(unsafe.Sizeof(HidAttributes{}))}
	ok, _, _ := hidDGetAttributes.Call(uintptr(h), uintptr(unsafe.Pointer(&a)))
	return a, ok != 0
}

// HidCaps is HIDP_CAPS.
type HidCaps struct {
	Usage                     uint16
	UsagePage                 uint16
	InputReportByteLength     uint16
	OutputReportByteLength    uint16
	FeatureReportByteLength   uint16
	Reserved                  [17]uint16
	NumberLinkCollectionNodes uint16
	NumberInputButtonCaps     uint16
	NumberInputValueCaps      uint16
	NumberInputDataIndices    uint16
	NumberOutputButtonCaps    uint16
	NumberOutputValueCaps     uint16
	NumberOutputDataIndices   uint16
	NumberFeatureButtonCaps   uint16
	NumberFeatureValueCaps    uint16
	NumberFeatureDataIndices  uint16
}

// GetCaps reads a HID collection's usage and report sizes.
func GetCaps(h windows.Handle) (HidCaps, bool) {
	var preparsed uintptr
	if ok, _, _ := hidDGetPreparsedData.Call(uintptr(h), uintptr(unsafe.Pointer(&preparsed))); ok == 0 {
		return HidCaps{}, false
	}
	defer hidDFreePreparsedData.Call(preparsed)
	var caps HidCaps
	const success = 0x00110000 // HIDP_STATUS_SUCCESS
	status, _, _ := hidPGetCaps.Call(preparsed, uintptr(unsafe.Pointer(&caps)))
	return caps, uint32(status) == success
}

// AttachConsole attaches this process to the console pid uses.
func AttachConsole(pid int) error {
	if ok, _, err := attachConsole.Call(uintptr(pid)); ok == 0 {
		return err
	}
	return nil
}

// FreeConsole detaches this process from its console.
func FreeConsole() { freeConsole.Call() }

// ConsoleWindow is the window of the console this process is attached to.
func ConsoleWindow() windows.HWND {
	h, _, _ := getConsoleWindow.Call()
	return windows.HWND(h)
}

// KeyEvent is a console KEY_EVENT INPUT_RECORD.
type KeyEvent struct {
	EventType    uint16
	_            uint16
	KeyDown      int32
	RepeatCount  uint16
	VirtualKey   uint16
	ScanCode     uint16
	Char         uint16
	ControlState uint32
}

// WriteConsoleInput adds key events to a console's input.
func WriteConsoleInput(h windows.Handle, events []KeyEvent) error {
	var written uint32
	ok, _, err := writeConsoleInput.Call(uintptr(h), uintptr(unsafe.Pointer(&events[0])),
		uintptr(len(events)), uintptr(unsafe.Pointer(&written)))
	if ok == 0 {
		return err
	}
	return nil
}

// ForegroundWindow is the window that has focus.
func ForegroundWindow() windows.HWND {
	h, _, _ := getForegroundWindow.Call()
	return windows.HWND(h)
}

// SetForegroundWindow brings a window forward.
func SetForegroundWindow(w windows.HWND) bool {
	ok, _, _ := setForegroundWindow.Call(uintptr(w))
	return ok != 0
}

// Owner is the window that owns w, or 0.
func Owner(w windows.HWND) windows.HWND {
	const owner = 4 // GW_OWNER
	h, _, _ := getWindow.Call(uintptr(w), owner)
	return windows.HWND(h)
}

// Restore shows a window again if it is minimized.
func Restore(w windows.HWND) {
	const restore = 9 // SW_RESTORE
	if minimized, _, _ := isIconic.Call(uintptr(w)); minimized != 0 {
		showWindow.Call(uintptr(w), restore)
	}
}

// KeyInput is a keyboard INPUT for SendInput.
type KeyInput struct {
	Type       uint32
	_          uint32
	VirtualKey uint16
	ScanCode   uint16
	Flags      uint32
	Time       uint32
	_          uint32
	ExtraInfo  uintptr
	_          [8]byte
}

// KeyUp marks a KeyInput as a release.
const KeyUp = 0x0002 // KEYEVENTF_KEYUP

// SendInput posts keyboard input to the system, as a keyboard would.
func SendInput(inputs []KeyInput) error {
	n, _, err := sendInput.Call(uintptr(len(inputs)), uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]))
	if int(n) != len(inputs) {
		return err
	}
	return nil
}
