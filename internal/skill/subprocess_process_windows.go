//go:build windows

package skill

import "os/exec"

func configureSubprocessCommand(*exec.Cmd) {}
