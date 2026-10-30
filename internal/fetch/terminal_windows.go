package fetch

import (
	"io"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

var consoleInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")

func terminalWidth(w io.Writer) (int, bool) {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("TERM") == "dumb" {
		return 0, false
	}
	var info struct {
		Size, Cursor struct{ X, Y int16 }
		Attributes   uint16
		Window       struct{ Left, Top, Right, Bottom int16 }
		MaxSize      struct{ X, Y int16 }
	}
	result, _, _ := consoleInfo.Call(f.Fd(), uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(f)
	if result == 0 {
		return 0, false
	}
	width := int(info.Window.Right-info.Window.Left) + 1
	if width < 1 {
		width = 80
	}
	return width, true
}
