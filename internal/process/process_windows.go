package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"unsafe"
)

const processMarker = "--slepok-managed-process-v1"

// Launch a supervisor in the same executable. It joins the job before
// creating the target, so even an immediately spawning target cannot escape
// assignment. This also works in test binaries importing this package.
func configure(cmd *exec.Cmd) {
	exe, err := os.Executable()
	if err != nil {
		cmd.Err = err
		return
	}
	target := cmd.Path
	cmd.Path = exe
	cmd.Args = append([]string{exe, processMarker, target}, cmd.Args[1:]...)
}

func SetIsolation(cmd *exec.Cmd, attr *syscall.SysProcAttr) { cmd.SysProcAttr = attr }

func init() {
	if len(os.Args) < 3 || os.Args[1] != processMarker {
		return
	}
	os.Exit(managedProcess(os.Args[2], os.Args[3:]))
}

var (
	kernel     = syscall.NewLazyDLL("kernel32.dll")
	createJob  = kernel.NewProc("CreateJobObjectW")
	setJobInfo = kernel.NewProc("SetInformationJobObject")
	assignJob  = kernel.NewProc("AssignProcessToJobObject")
)

type jobBasicLimits struct {
	ProcessTime, JobTime         int64
	Flags                        uint32
	MinWorkingSet, MaxWorkingSet uintptr
	ActiveProcesses              uint32
	Affinity                     uintptr
	Priority, Scheduling         uint32
}

type jobLimits struct {
	Basic                                                      jobBasicLimits
	IO                                                         [6]uint64
	ProcessMemory, JobMemory, PeakProcessMemory, PeakJobMemory uintptr
}

func setLimits(job syscall.Handle, killOnClose bool) error {
	var limits jobLimits
	if killOnClose {
		limits.Basic.Flags = 0x2000
	} // JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	ok, _, err := setJobInfo.Call(uintptr(job), 9, uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits))
	runtime.KeepAlive(&limits)
	if ok == 0 {
		return fmt.Errorf("set process job limits: %w", err)
	}
	return nil
}

func managedProcess(name string, args []string) int {
	h, _, err := createJob.Call(0, 0)
	if h == 0 {
		fmt.Fprintln(os.Stderr, "slepok: create process job:", err)
		return 1
	}
	job := syscall.Handle(h)
	if err := setLimits(job, true); err != nil {
		syscall.CloseHandle(job)
		fmt.Fprintln(os.Stderr, "slepok:", err)
		return 1
	}
	// The job handle is not inheritable; only this supervisor owns it.
	ok, _, err := assignJob.Call(h, ^uintptr(0)) // current-process pseudo handle
	if ok == 0 {
		syscall.CloseHandle(job)
		fmt.Fprintln(os.Stderr, "slepok: assign process job:", err)
		return 1
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else {
			fmt.Fprintln(os.Stderr, "slepok: start tool:", err)
			code = 1
		}
	}
	// The tool has finished normally. Clear the self-killing flag before
	// closing the job so its exit status can be relayed unchanged. On
	// cancellation this code is not reached: terminating the supervisor
	// closes the last job handle and kills the still-running process tree.
	if err := setLimits(job, false); err != nil {
		fmt.Fprintln(os.Stderr, "slepok:", err)
		// Do not close a self-killing handle explicitly. Process teardown
		// releases it; this is an error path, not successful tool completion.
		return 1
	}
	syscall.CloseHandle(job)
	return code
}
