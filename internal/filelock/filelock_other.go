//go:build !linux

package filelock

import (
	"errors"
	"os"
)

func try(*os.File, bool) (bool, error) {
	return false, errors.New("process locking is currently supported only on Linux")
}
