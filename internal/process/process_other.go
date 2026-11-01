//go:build !linux && !windows && !darwin

package process

import (
	"os/exec"
	"syscall"
)

func configure(*exec.Cmd) {}

func configureInteractive(cmd *exec.Cmd) { configure(cmd) }

func SetIsolation(cmd *exec.Cmd, attr *syscall.SysProcAttr) { cmd.SysProcAttr = attr }
