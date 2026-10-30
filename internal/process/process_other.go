//go:build !linux && !windows

package process

import (
	"os/exec"
	"syscall"
)

func configure(*exec.Cmd) {}

func SetIsolation(cmd *exec.Cmd, attr *syscall.SysProcAttr) { cmd.SysProcAttr = attr }
