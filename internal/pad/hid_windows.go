package pad

import (
	"errors"
	"strings"
	"sync"

	"golang.org/x/sys/windows"

	"github.com/dustinwdev/codex-micro-for-claude-code/internal/win32"
)

// Find lists the supported pads attached to this computer. Windows lists
// each of a pad's HID collections apart: the one for the host protocol is
// the one that takes reports as long as ours.
func Find() []Found {
	guid := win32.HidGUID()
	paths, err := windows.CM_Get_Device_Interface_List("", &guid, windows.CM_GET_DEVICE_INTERFACE_LIST_PRESENT)
	if err != nil {
		return nil
	}
	var found []Found
	for _, path := range paths {
		h, err := openPath(path, 0)
		if err != nil {
			continue
		}
		attrs, ok := win32.GetAttributes(h)
		caps, capsOK := win32.GetCaps(h)
		windows.CloseHandle(h)
		model, known := models[attrs.ProductID]
		if !ok || !capsOK || !known || attrs.VendorID != vendorID || caps.OutputReportByteLength < reportSize {
			continue
		}
		transport := "usb"
		// Bluetooth LE pads sit under the HID-over-GATT service, 0x1812.
		if lower := strings.ToLower(path); strings.Contains(lower, "00001812-") || strings.Contains(lower, "bth") {
			transport = "bluetooth"
		}
		found = append(found, Found{Model: model, Transport: transport, path: path})
	}
	return found
}

// AccessProblem says why this user can't open the pad, or "" when they can.
func AccessProblem(f Found) string {
	h, err := openPath(f.path, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err != nil {
		return openError(err).Error()
	}
	windows.CloseHandle(h)
	return ""
}

func openPath(path string, access uint32) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	return windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil, windows.OPEN_EXISTING, 0, 0)
}

func openError(err error) error {
	if errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return errors.New("another app holds the pad: quit the ChatGPT app")
	}
	return err
}

// hidDevice reads and writes through handles of its own: on one handle, a
// write would wait for the read in progress.
type hidDevice struct {
	reader, writer windows.Handle
	outputLength   int
	closed         sync.Once
}

func open(f Found, onReport func([]byte), onLost func()) (transport, error) {
	writer, err := openPath(f.path, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err != nil {
		return nil, openError(err)
	}
	reader, err := openPath(f.path, windows.GENERIC_READ|windows.GENERIC_WRITE)
	if err != nil {
		windows.CloseHandle(writer)
		return nil, openError(err)
	}
	caps, _ := win32.GetCaps(writer)
	h := &hidDevice{reader: reader, writer: writer, outputLength: int(caps.OutputReportByteLength)}
	go func() {
		buf := make([]byte, max(int(caps.InputReportByteLength), reportSize))
		for {
			var n uint32
			if err := windows.ReadFile(reader, buf, &n, nil); err != nil {
				// Unplugged, asleep or out of range; or closed by us.
				h.close()
				onLost()
				return
			}
			onReport(buf[:n])
		}
	}()
	return h, nil
}

func (h *hidDevice) write(report []byte) error {
	// Windows takes reports of exactly the collection's output length.
	out := make([]byte, h.outputLength)
	copy(out, report)
	var n uint32
	return windows.WriteFile(h.writer, out, &n, nil)
}

func (h *hidDevice) close() {
	h.closed.Do(func() {
		windows.CancelIoEx(h.reader, nil)
		windows.CloseHandle(h.reader)
		windows.CloseHandle(h.writer)
	})
}
