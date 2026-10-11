//go:build !unix

package git

import "os/exec"

func killProcessGroup(cmd *exec.Cmd) {}
