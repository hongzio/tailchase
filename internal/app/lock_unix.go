//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package app

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

// Keep the inode stable. The OS releases this lock even after SIGKILL.
func acquireLock(path string) (func(), error) {
	f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	for i := 0; i < 40; i++ {
		e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
		}
		if !errors.Is(e, syscall.EWOULDBLOCK) && !errors.Is(e, syscall.EAGAIN) {
			f.Close()
			return nil, e
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.Close()
	return nil, fmt.Errorf("%w: %s", errStoreLocked, path)
}
