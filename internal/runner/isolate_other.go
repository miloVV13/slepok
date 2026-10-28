//go:build !linux

package runner

import "syscall"

// isolation is not available outside Linux; nil makes Build run without it
// and report so.
func isolation() *syscall.SysProcAttr { return nil }

func isolationUnavailable(error) bool { return false }
