//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly && !windows

package app

import "os/exec"

func detachDaemon(cmd *exec.Cmd) {}
