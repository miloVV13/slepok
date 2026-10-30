//go:build !linux && !windows && !darwin

package filelock

import (
	"errors"
	"os"
)

func try(*os.File, bool) (bool, error) {
	return false, errors.New("process locking is not implemented on this platform")
}
