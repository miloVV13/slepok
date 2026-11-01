//go:build !windows

package runner

func longWindowsPath(p string) string { return p }

func shortWindowsDirectory(p string) string { return p }
