//go:build unix

package git

import (
	"os/exec"
	"syscall"
)

// killProcessGroup runs cmd in its own process group and kills the whole group
// on cancel. git starts helpers such as git-remote-http that would otherwise
// outlive a killed git and keep connections open.
func killProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
