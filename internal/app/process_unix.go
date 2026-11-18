//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package app

import (
	"os/exec"
	"syscall"
)

func detachDaemon(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
