package investment

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func configureOpenDProcess(cmd *exec.Cmd) {
	// OpenD's own monitor is disabled. The direct child also dies if Workbench
	// is killed before its normal Close path can run.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}

func cleanupOpenDProcess(cmd *exec.Cmd) {
	// Reap any helpers that survived their leader's normal or forced exit.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
