//go:build !linux && !windows && !darwin

package process

import (
	"os/exec"
	"syscall"
)

func configure(*exec.Cmd) {}

func SetIsolation(cmd *exec.Cmd, attr *syscall.SysProcAttr) { cmd.SysProcAttr = attr }
