//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd && !dragonfly

package app

import (
	"errors"
	"fmt"
	"os"
	"time"
)

func acquireLock(path string) (func(), error) {
	for i := 0; i < 40; i++ {
		e := os.Mkdir(path, 0700)
		if e == nil {
			return func() { _ = os.Remove(path) }, nil
		}
		if !errors.Is(e, os.ErrExist) {
			return nil, e
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil, fmt.Errorf("%w: %s; after a crash remove it when no tailchase process is running", errStoreLocked, path)
}
