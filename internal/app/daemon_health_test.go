package app

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDaemonKeepsHeartbeatAndRecoversAfterConfigurationError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("helper uses POSIX shutdown")
	}
	s := Store{Dir: t.TempDir()}
	if e := s.Update(func(c *Config) error { return nil }); e != nil {
		t.Fatal(e)
	}
	valid, e := os.ReadFile(filepath.Join(s.Dir, "config.json"))
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDaemonHelper$")
	cmd.Env = append(os.Environ(), "TAILCHASE_TEST_DAEMON=1")
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	if e = s.startBackgroundDaemon(cmd); e != nil {
		t.Fatal(e)
	}
	await := func(check func(Runtime) bool) Runtime {
		t.Helper()
		deadline := time.Now().Add(6 * time.Second)
		for time.Now().Before(deadline) {
			r, e := s.runtime()
			if e == nil && check(r) {
				return r
			}
			time.Sleep(30 * time.Millisecond)
		}
		t.Fatal("daemon health did not update")
		return Runtime{}
	}
	r := await(func(r Runtime) bool { return r.BuildID != "" && r.ConfigError == "" })
	if len(r.BuildID) != 64 {
		t.Fatal("missing executable build identity")
	}
	if e = atomicWrite(filepath.Join(s.Dir, "config.json"), []byte(`{"version":1,"SECRET_NOT_FOR_LOGS":true}`)); e != nil {
		t.Fatal(e)
	}
	bad := await(func(r Runtime) bool { return strings.Contains(r.ConfigError, "unsupported fields") })
	await(func(r Runtime) bool { return r.Updated.After(bad.Updated) })
	var status bytes.Buffer
	if e = s.status(&status); e == nil || !strings.Contains(status.String(), "running with configuration error") || strings.Contains(status.String(), "offline") {
		t.Fatal("invalid configuration was shown as offline", e, status.String())
	}
	if strings.Contains(bad.ConfigError, "SECRET_NOT_FOR_LOGS") {
		t.Fatal("configuration contents leaked")
	}
	if e = s.startBackgroundDaemon(exec.Command("no-such-daemon")); e == nil || !strings.Contains(e.Error(), "configuration error") {
		t.Fatal("unhealthy daemon reported success", e)
	}
	if e = atomicWrite(filepath.Join(s.Dir, "config.json"), valid); e != nil {
		t.Fatal(e)
	}
	await(func(r Runtime) bool { return r.ConfigError == "" })
	if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
		t.Fatal(e)
	}
}
func TestDaemonStartupExplainsStaleLocksAndOutdatedBuilds(t *testing.T) {
	for _, test := range []struct {
		name, build, want string
		stale             bool
	}{{"stale", "", "unresponsive", true}, {"old-build", "outdated", "older/different", false}, {"legacy", "", "older/different", false}} {
		t.Run(test.name, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			r := Runtime{PID: 123, BuildID: test.build, Updated: time.Now(), Services: map[string]State{}}
			if test.stale {
				r.Updated = r.Updated.Add(-time.Minute)
			}
			data, _ := json.Marshal(r)
			if e := atomicWrite(filepath.Join(s.Dir, "runtime.json"), data); e != nil {
				t.Fatal(e)
			}
			if test.stale {
				unlock, e := s.lock("daemon")
				if e != nil {
					t.Fatal(e)
				}
				defer unlock()
			}
			e := s.startBackgroundDaemon(exec.Command("no-such-daemon"))
			if e == nil || !strings.Contains(e.Error(), test.want) {
				t.Fatal("startup did not explain existing daemon", e)
			}
		})
	}
}
