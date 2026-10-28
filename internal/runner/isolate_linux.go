package runner

import (
	"errors"
	"os"
	"syscall"
)

// isolation starts the process in new user and network namespaces. The
// current uid and gid are mapped to themselves, so the build does not run
// as namespace root and cannot override file permissions (spec 17, stage 2
// finding 2). The new network namespace has no interfaces but loopback.
func isolation() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Cloneflags:                 syscall.CLONE_NEWUSER | syscall.CLONE_NEWNET,
		UidMappings:                []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}},
		GidMappings:                []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}},
		GidMappingsEnableSetgroups: false,
	}
}

// isolationUnavailable reports whether starting with isolation failed
// because the host does not allow unprivileged user namespaces.
func isolationUnavailable(err error) bool {
	return errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) ||
		errors.Is(err, syscall.EINVAL) || errors.Is(err, syscall.ENOSPC) || errors.Is(err, syscall.ENOSYS)
}
