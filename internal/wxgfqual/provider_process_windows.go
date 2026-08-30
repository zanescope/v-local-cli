//go:build windows

package wxgfqual

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createProviderJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS |
		windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY |
		windows.JOB_OBJECT_LIMIT_JOB_MEMORY |
		windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE |
		windows.JOB_OBJECT_LIMIT_DIE_ON_UNHANDLED_EXCEPTION
	limits.BasicLimitInformation.ActiveProcessLimit = providerActiveProcessLimit
	limits.ProcessMemoryLimit = providerProcessMemoryLimitBytes
	limits.JobMemoryLimit = providerJobMemoryLimitBytes
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

func resumeProviderMainThread(processID uint32) error {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snapshot, &entry); err != nil {
		return err
	}
	for {
		if entry.OwnerProcessID == processID {
			thread, openErr := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
			if openErr != nil {
				return openErr
			}
			_, resumeErr := windows.ResumeThread(thread)
			_ = windows.CloseHandle(thread)
			return resumeErr
		}
		if err := windows.Thread32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return errors.New("provider main thread not found")
			}
			return err
		}
	}
}

func stopStartedProvider(command *exec.Cmd, job windows.Handle) {
	_ = windows.TerminateJobObject(job, 1)
	if command.Process != nil {
		_ = command.Process.Kill()
	}
	_ = command.Wait()
}

func runProviderCommand(command *exec.Cmd) (ProviderIsolation, error) {
	isolation := ProviderIsolation{
		Method:                     "windows_job_object_suspended_assignment",
		CreateProcessTreeContained: true,
		JobMemberMemoryLimited:     true,
		ProcessMemoryLimitBytes:    providerProcessMemoryLimitBytes,
		JobMemoryLimitBytes:        providerJobMemoryLimitBytes,
		ActiveProcessLimit:         providerActiveProcessLimit,
	}
	job, err := createProviderJob()
	if err != nil {
		return ProviderIsolation{}, err
	}
	defer windows.CloseHandle(job)
	attributes := syscall.SysProcAttr{}
	if command.SysProcAttr != nil {
		attributes = *command.SysProcAttr
	}
	attributes.CreationFlags |= windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW
	attributes.HideWindow = true
	command.SysProcAttr = &attributes
	if err := command.Start(); err != nil {
		return ProviderIsolation{}, err
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(command.Process.Pid))
	if err != nil {
		stopStartedProvider(command, job)
		return ProviderIsolation{}, err
	}
	assignErr := windows.AssignProcessToJobObject(job, process)
	_ = windows.CloseHandle(process)
	if assignErr != nil {
		stopStartedProvider(command, job)
		return ProviderIsolation{}, assignErr
	}
	if err := resumeProviderMainThread(uint32(command.Process.Pid)); err != nil {
		stopStartedProvider(command, job)
		return ProviderIsolation{}, err
	}
	return isolation, command.Wait()
}
