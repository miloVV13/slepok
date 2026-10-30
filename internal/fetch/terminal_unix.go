//go:build linux || darwin

package fetch

import (
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func terminalWidth(w io.Writer) (int, bool) {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("TERM") == "dumb" {
		return 0, false
	}
	var size struct{ Rows, Cols, X, Y uint16 }
	_, _, err := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&size)))
	runtime.KeepAlive(f)
	if err != 0 {
		return 0, false
	}
	width := int(size.Cols)
	if width == 0 {
		width = 80
	}
	return width, true
}
