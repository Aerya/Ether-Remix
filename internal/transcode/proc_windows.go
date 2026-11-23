//go:build windows

package transcode

import (
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// createNoWindow keeps ffmpeg from flashing a console when Ether runs as a
// service or tray app.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// Windows has no SIGSTOP. Without a pause the throttle had to kill ffmpeg
// every time it got four segments ahead and start a new one (a fresh source
// open, probe and encoder init) a few seconds later: a 4K transcode on the
// Windows server restarted every 16 s and stalled the player each time.
// NtSuspendProcess / NtResumeProcess freeze and thaw every thread of the
// process, which is what SIGSTOP/SIGCONT do on Unix.
var (
	ntdll            = windows.NewLazySystemDLL("ntdll.dll")
	ntSuspendProcess = ntdll.NewProc("NtSuspendProcess")
	ntResumeProcess  = ntdll.NewProc("NtResumeProcess")
)

const processSuspendResume = 0x0800

func suspend(p *os.Process) error {
	if ntSuspendProcess.Find() != nil || ntResumeProcess.Find() != nil {
		return ErrPauseUnsupported
	}
	return callProcess(ntSuspendProcess, p)
}

func resume(p *os.Process) error {
	if ntResumeProcess.Find() != nil {
		return nil
	}
	return callProcess(ntResumeProcess, p)
}

func callProcess(proc *windows.LazyProc, p *os.Process) error {
	h, err := windows.OpenProcess(processSuspendResume, false, uint32(p.Pid))
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	if status, _, _ := proc.Call(uintptr(h)); status != 0 {
		return windows.NTStatus(status)
	}
	return nil
}
