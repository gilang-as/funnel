//go:build !windows

package cmd

import (
	"os/exec"
	"syscall"
)

func spawnDaemon(exe string) error {
	cmd := exec.Command(exe, "daemon")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd.Start()
}
