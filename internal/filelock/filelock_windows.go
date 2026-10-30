package filelock

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

// kernel32 is a Windows KnownDLL. Keep the call local rather than add a
// dependency for a single API. See LockFileEx in Microsoft's fileapi docs.
var lockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

func try(f *os.File, shared bool) (bool, error) {
	const (
		failImmediately               = 1
		exclusiveLock                 = 2
		lockViolation   syscall.Errno = 33
	)
	flags := uintptr(failImmediately)
	if !shared {
		flags |= exclusiveLock
	}
	// Fd returns a synchronous handle. Lock the first byte, even though
	// the lock file is empty; Windows permits locking beyond EOF.
	overlap := new(syscall.Overlapped)
	ok, _, err := lockFileEx.Call(f.Fd(), flags, 0, 1, 0, uintptr(unsafe.Pointer(overlap)))
	runtime.KeepAlive(overlap)
	runtime.KeepAlive(f)
	if ok != 0 {
		return true, nil
	}
	if errors.Is(err, lockViolation) {
		return false, nil
	}
	return false, err
}
