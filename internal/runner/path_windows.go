package runner

import (
	"path/filepath"
	"strings"
	"syscall"
)

// CMake may choose existing 8.3 tool names to avoid spaces. Ask Windows for
// their long names before checking store boundaries; never guess from '~'.
// An unresolved path stays unchanged and is checked conservatively.
func longWindowsPath(p string) string {
	if !strings.Contains(p, "~") {
		return p
	}
	name, err := syscall.UTF16PtrFromString(filepath.FromSlash(p))
	if err != nil {
		return p
	}
	buffer := make([]uint16, 260)
	for attempts := 0; attempts < 2; attempts++ {
		n, err := syscall.GetLongPathName(name, &buffer[0], uint32(len(buffer)))
		if err != nil || n == 0 {
			return p
		}
		if n < uint32(len(buffer)) {
			return syscall.UTF16ToString(buffer[:n])
		}
		if n > 32768 {
			return p
		}
		buffer = make([]uint16, int(n)+1)
	}
	return p
}
