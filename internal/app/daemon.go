package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

type State struct {
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	NextRetry time.Time `json:"next_retry,omitempty"`
}
type Runtime struct {
	BuildID     string            `json:"build_id,omitempty"`
	ConfigError string            `json:"config_error,omitempty"`
	PID         int               `json:"pid"`
	Updated     time.Time         `json:"updated"`
	Services    map[string]State  `json:"services"`
	Addresses   map[string]string `json:"addresses,omitempty"`
}
type child struct {
	signature string
	cmd       *exec.Cmd
	done      chan error
	cleanup   func()
	cancel    context.CancelFunc
	state     State
	started   time.Time
}

func (s Store) command(c Config, v Service) ([]string, error) {
	r := c.Remotes[v.Remote]
	key, err := s.serviceIdentity(c, v)
	if err != nil {
		return nil, err
	}
	flag := "--key=new"
	if key != "" {
		flag = "--key=" + key
	}
	args := []string{v.Kind, flag}
	if v.Kind == "forward" {
		if aliasAddress(r) == "" {
			return nil, fmt.Errorf("forward requires a TC address alias")
		}
		bind := v.Bind
		if bind == "" {
			bind = "127.0.0.1"
		}
		args = append(args, "--bind="+bind, aliasAddress(r))
		args = append(args, v.Ports...)
		return args, nil
	}
	args = append(args, "--json")
	if !v.AllowAnyone {
		pub := append([]string(nil), v.PublicKeys...)
		for _, a := range v.Allow {
			p := peerPublic(c, a)
			if p == "" {
				return nil, fmt.Errorf("missing allow identity for %s", a)
			}
			pub = append(pub, p)
		}
		slices.Sort(pub)
		pub = slices.Compact(pub)
		allow := strings.Join(pub, ",")
		if allow == "" {
			allow = "none"
		}
		args = append(args, "--allow="+allow)
	}
	if v.SSHAuthorizedKeys != "" {
		args = append(args, "--ssh-authorized-keys="+v.SSHAuthorizedKeys)
	}
	return append(args, v.Ports...), nil
}
func signature(c Config, v Service, args []string) string {
	key := strings.TrimPrefix(args[1], "--key=")
	data, _ := json.Marshal([]any{args, c.Keys[key]})
	return string(data)
}
func stopChild(p *child) {
	if p.cmd != nil {
		p.cancel()
		<-p.done
		p.cleanup()
		p.cmd = nil
	}
}
func (s Store) daemon() error {
	unlock, e := s.lock("daemon")
	if e != nil {
		return e
	}
	defer unlock()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	children := map[string]*child{}
	addresses := map[string]string{}
	var mu sync.Mutex
	defer func() {
		for _, p := range children {
			stopChild(p)
		}
		os.Remove(filepath.Join(s.Dir, "runtime.json"))
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	buildID := currentBuildID()
	lastConfigError := ""
	reconcile := func() {
		c, err := s.Load()
		if err != nil {
			message := configurationError(err)
			if message != lastConfigError {
				fmt.Fprintln(os.Stderr, message+"; keeping current services")
			}
			lastConfigError = message
			report := Runtime{PID: os.Getpid(), BuildID: buildID, Updated: time.Now(), ConfigError: message, Services: map[string]State{}, Addresses: map[string]string{}}
			for id, p := range children {
				report.Services[id] = p.state
			}
			mu.Lock()
			for id, address := range addresses {
				report.Addresses[id] = address
			}
			mu.Unlock()
			s.writeRuntime(report)
			return
		}
		lastConfigError = ""
		for n, p := range children {
			v, ok := c.Services[n]
			if !ok || !v.Enabled {
				stopChild(p)
				delete(children, n)
				mu.Lock()
				delete(addresses, n)
				mu.Unlock()
			}
		}
		for n, v := range c.Services {
			if !v.Enabled {
				continue
			}
			args, err := s.command(c, v)
			if err != nil {
				if p := children[n]; p != nil {
					stopChild(p)
				}
				children[n] = &child{state: State{State: "blocked", Error: err.Error()}}
				continue
			}
			sig := signature(c, v, args)
			p := children[n]
			if p != nil && p.signature != sig {
				stopChild(p)
				delete(children, n)
				p = nil
				mu.Lock()
				delete(addresses, n)
				mu.Unlock()
			}
			if p == nil {
				p = &child{signature: sig}
				children[n] = p
			}
			if p.cmd != nil {
				select {
				case exit := <-p.done:
					p.cancel()
					p.cleanup()
					p.cmd = nil
					if time.Since(p.started) > time.Minute {
						p.state.Attempts = 0
					}
					p.state.Attempts++
					p.state.State = "retrying"
					p.state.Error = "process exited"
					if exit != nil {
						p.state.Error = "tailcat exited unsuccessfully"
					}
					delay := time.Second * time.Duration(1<<min(p.state.Attempts-1, 5))
					p.state.NextRetry = time.Now().Add(delay)
					mu.Lock()
					delete(addresses, n)
					mu.Unlock()
				default:
					continue
				}
			}
			if time.Now().Before(p.state.NextRetry) {
				continue
			}
			key, err := s.serviceIdentity(c, v)
			clean := func() {}
			if err == nil && key != "" {
				var path string
				path, clean, err = s.materialize(c.Keys[key])
				if err == nil {
					args[1] = "--key=" + path
				}
			}
			if err != nil {
				p.state = State{State: "blocked", Error: "cannot prepare client/server identity"}
				continue
			}

			childCtx, childCancel := context.WithCancel(ctx)
			cmd := exec.CommandContext(childCtx, "tailcat", args...)
			cmd.Stderr = io.Discard
			cmd.WaitDelay = 2 * time.Second
			var scanDone chan struct{}
			if v.Kind == "serve" {
				reader, writer := io.Pipe()
				cmd.Stdout = writer
				scanDone = make(chan struct{})
				id := n
				go func() {
					defer close(scanDone)
					defer reader.Close()
					scanner := bufio.NewScanner(reader)
					for scanner.Scan() {
						var record struct {
							ListenAddr string `json:"listenAddr"`
						}
						if json.Unmarshal(scanner.Bytes(), &record) == nil && record.ListenAddr != "" {
							mu.Lock()
							addresses[id] = record.ListenAddr
							mu.Unlock()
						}
					}
				}()
				// Close the pipe after Wait to release the scanner.
				p.cleanup = func() { writer.Close(); <-scanDone; clean() }
			} else {
				cmd.Stdout = io.Discard
				p.cleanup = clean
			}
			if err = cmd.Start(); err != nil {
				childCancel()
				p.cleanup()
				p.state = State{State: "retrying", Error: "cannot start tailcat; check installation", NextRetry: time.Now().Add(5 * time.Second)}
				continue
			}
			p.cmd = cmd
			p.cancel = childCancel
			p.started = time.Now()
			p.done = make(chan error, 1)
			p.state.State = "running"
			p.state.Error = ""
			p.state.NextRetry = time.Time{}
			go func(done chan error) { done <- cmd.Wait() }(p.done)
		}
		rt := Runtime{PID: os.Getpid(), BuildID: buildID, Updated: time.Now(), Services: map[string]State{}, Addresses: map[string]string{}}
		for n, v := range c.Services {
			st := State{State: "disabled"}
			if v.Enabled {
				st = children[n].state
			}
			rt.Services[n] = st
		}
		mu.Lock()
		for n, a := range addresses {
			rt.Addresses[n] = a
		}
		mu.Unlock()

		s.writeRuntime(rt)
	}
	reconcile()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			reconcile()
		}
	}
}
func (s Store) runtime() (Runtime, error) {
	b, e := os.ReadFile(filepath.Join(s.Dir, "runtime.json"))
	if e != nil {
		return Runtime{}, e
	}
	var r Runtime
	e = json.Unmarshal(b, &r)
	if e != nil {
		return r, e
	}
	if time.Since(r.Updated) > 5*time.Second {
		return r, errors.New("daemon status is stale")
	}
	return r, nil
}
func (s Store) status(out io.Writer) error {
	r, err := s.runtime()
	if err != nil {
		if r.PID > 0 {
			fmt.Fprintf(out, "daemon: offline or unresponsive (last PID %d; restart may be required)\n", r.PID)
		} else {
			fmt.Fprintln(out, "daemon: offline or stale")
		}
	} else if r.ConfigError != "" {
		fmt.Fprintln(out, "daemon: running with configuration error:", r.ConfigError)
	} else {
		fmt.Fprintln(out, "daemon: running")
	}
	c, e := s.Load()
	if e != nil {
		return fmt.Errorf("configuration rejected: %s", configurationError(e))
	}

	for _, n := range names(c.Services) {
		v := c.Services[n]
		state := "disabled"
		if v.Enabled {
			state = "pending"
		}
		if st, ok := r.Services[n]; ok && err == nil {
			state = st.State
			if st.Error != "" {
				state += " (" + st.Error + ")"
			}
		}
		fmt.Fprintf(out, "%s\t%s\tenabled=%t\t%s\n", n, v.Kind, v.Enabled, state)
	}
	return nil
}
