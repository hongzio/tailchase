package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func (s Store) startBackgroundDaemon(cmd *exec.Cmd) error {
	r, runtimeErr := s.runtime()
	if runtimeErr == nil {
		if r.BuildID == "" || currentBuildID() != "" && r.BuildID != currentBuildID() {
			return fmt.Errorf("daemon PID %d uses an older/different build; stop it and start the daemon again with the current binary", r.PID)
		}
		if r.ConfigError != "" {
			return fmt.Errorf("daemon is running with a configuration error: %s", r.ConfigError)
		}
		return nil
	}
	if r.PID > 0 {
		unlock, err := s.lock("daemon")
		if errors.Is(err, errStoreLocked) {
			return fmt.Errorf("existing daemon is unresponsive (last PID %d) and still holds its lock; stop it before starting the current binary", r.PID)
		}
		if err != nil {
			return err
		}
		unlock()
	}
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(s.Dir, "daemon.log"), os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	env := cmd.Environ()
	filtered := make([]string, 0, len(env)+1)
	for _, v := range env {
		if !strings.HasPrefix(v, "TAILCHASE_CONFIG_DIR=") && !strings.HasPrefix(v, "TAILCHASE_TAILCAT_KEYS_DIR=") {
			filtered = append(filtered, v)
		}
	}
	// Resolve relative paths before detaching so the same store is always used.
	dir, err := filepath.Abs(s.Dir)
	if err != nil {
		return err
	}
	cmd.Env = append(filtered, "TAILCHASE_CONFIG_DIR="+dir, "TAILCHASE_TAILCAT_KEYS_DIR="+s.NativeDir)
	detachDaemon(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(50 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-poll.C:
			if ready, err := s.runtime(); err == nil {
				if ready.ConfigError != "" {
					return fmt.Errorf("daemon started but configuration is invalid: %s", ready.ConfigError)
				}
				return nil
			}
		case err := <-done:
			if _, e := s.runtime(); e == nil {
				return nil
			}
			return fmt.Errorf("daemon exited before becoming ready (%v); see daemon.log", err)
		case <-deadline.C:
			_ = cmd.Process.Kill()
			<-done
			return fmt.Errorf("daemon did not become ready; see daemon.log")
		}
	}
}
