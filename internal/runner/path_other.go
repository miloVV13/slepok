//go:build !windows

package runner

func longWindowsPath(p string) string { return p }
