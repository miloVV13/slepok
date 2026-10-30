//go:build !windows

package runner

import (
	"os"
	"path/filepath"
)

func prepareShell(bin, cmake string, configure []string) (string, []string, []string, error) {
	if err := os.WriteFile(filepath.Join(bin, "cmake"), []byte(cmakeWrapper(cmake, configure)), 0o755); err != nil {
		return "", nil, nil, err
	}
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/sh"
	}
	return sh, nil, nil, nil
}
