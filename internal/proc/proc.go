package proc

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const createNoWindow = 0x08000000

func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWindow(cmd)
	return cmd
}

func Command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	return cmd
}

func LookPath(file string) (string, error) {
	return exec.LookPath(file)
}

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}

var (
	jobOnce   sync.Once
	jobHandle windows.Handle
	jobErr    error
)

// killJob lazily creates a process-wide Windows Job Object configured with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE. We never close the handle ourselves, so
// it only closes when wintp.exe's own handle table is torn down by the OS on
// exit - including a crash or a forced kill. At that point Windows kills
// every process assigned to the job, so a mount's rclone subprocess can never
// outlive the app as an orphan.
func killJob() (windows.Handle, error) {
	jobOnce.Do(func() {
		h, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			jobErr = fmt.Errorf("create job object: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
				LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
			},
		}
		if _, err := windows.SetInformationJobObject(
			h,
			windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)),
			uint32(unsafe.Sizeof(info)),
		); err != nil {
			_ = windows.CloseHandle(h)
			jobErr = fmt.Errorf("configure job object: %w", err)
			return
		}
		jobHandle = h
	})
	return jobHandle, jobErr
}

// AssignToKillJob ties an already-started cmd to the process-wide kill job,
// so the OS terminates it automatically if wintp.exe disappears without a
// chance to clean up (crash, "End task", power loss). Call it right after
// cmd.Start(). Failure is non-fatal: the process keeps running, just without
// that safety net.
func AssignToKillJob(cmd *exec.Cmd) error {
	job, err := killJob()
	if err != nil {
		return err
	}
	if cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		return fmt.Errorf("open process: %w", err)
	}
	defer windows.CloseHandle(h)
	return windows.AssignProcessToJobObject(job, h)
}
