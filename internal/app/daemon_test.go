package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonHelper(t *testing.T) {
	if os.Getenv("TAILCHASE_TEST_DAEMON") != "1" {
		return
	}
	s, e := DefaultStore()
	if e == nil {
		e = s.daemon()
	}
	if e != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
func TestDaemonLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake tailcat uses POSIX shell")
	}
	s := Store{Dir: t.TempDir()}
	fake := t.TempDir()
	trace := filepath.Join(fake, "trace")
	script := "#!/bin/sh\necho start $$ >> '" + trace + "'\nprintf '{\"listenAddr\":\"test-address\"}\\n'\nexec sleep 60\n"
	if e := os.WriteFile(filepath.Join(fake, "tailcat"), []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	c := fixture()
	if e := s.Update(func(dst *Config) error { *dst = c; return nil }); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonHelper$")
	cmd.Env = append(os.Environ(), "TAILCHASE_TEST_DAEMON=1", "TAILCHASE_CONFIG_DIR="+s.Dir, "PATH="+fake+string(os.PathListSeparator)+os.Getenv("PATH"))
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	defer func() { _ = cmd.Process.Kill() }()
	await := func(fn func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if fn() {
				return
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("daemon did not reach expected state")
	}
	await(func() bool {
		r, e := s.runtime()
		return e == nil && r.Services["web"].State == "running" && r.Addresses["web"] == "test-address"
	})

	first, _ := os.ReadFile(trace)
	fields := strings.Fields(string(first))
	pid, e := strconv.Atoi(fields[len(fields)-1])
	if e != nil {
		t.Fatal(e)
	}
	proc, e := os.FindProcess(pid)
	if e != nil {
		t.Fatal(e)
	}
	if e = proc.Kill(); e != nil {
		t.Fatal(e)
	}
	await(func() bool {
		r, e := s.runtime()
		return e == nil && r.Services["web"].State == "retrying" && r.Services["web"].Attempts == 1
	})
	await(func() bool { b, _ := os.ReadFile(trace); return strings.Count(string(b), "start") >= 2 })
	if e := s.Update(func(c *Config) error { v := c.Services["web"]; v.Allow = nil; c.Services["web"] = v; return nil }); e != nil {
		t.Fatal(e)
	}
	await(func() bool { b, _ := os.ReadFile(trace); return strings.Count(string(b), "start") >= 3 })
	if e := s.Update(func(c *Config) error { v := c.Services["web"]; v.Enabled = false; c.Services["web"] = v; return nil }); e != nil {
		t.Fatal(e)
	}
	await(func() bool {
		r, e := s.runtime()
		return e == nil && r.Services["web"].State == "disabled" && len(r.Addresses) == 0
	})
	if e := cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("daemon did not stop")
	}
	files, _ := filepath.Glob(filepath.Join(s.Dir, ".key-*"))
	if len(files) > 0 {
		t.Fatal("temporary private keys not cleaned")
	}
}
