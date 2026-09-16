//go:build !linux

package investment

import (
	"os"
	"os/exec"
)

func configureOpenDProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
}
func cleanupOpenDProcess(*exec.Cmd) {}
