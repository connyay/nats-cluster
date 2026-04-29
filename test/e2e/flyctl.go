package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// flyRun runs the fly CLI with args, streaming output to stderr. Blocks until done.
func flyRun(ctx context.Context, args ...string) error {
	log.Printf("$ fly %s", strings.Join(args, " "))
	cmd := exec.CommandContext(ctx, "fly", args...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// flyJSON runs fly and parses stdout into dst.
func flyJSON(ctx context.Context, dst any, args ...string) error {
	cmd := exec.CommandContext(ctx, "fly", args...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("fly %s: %w", strings.Join(args, " "), err)
	}
	return json.Unmarshal(stdout.Bytes(), dst)
}

type flyMachine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	State  string `json:"state"`
	Region string `json:"region"`
	Checks []struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	} `json:"checks"`
}

func listMachines(ctx context.Context, app string) ([]flyMachine, error) {
	var ms []flyMachine
	if err := flyJSON(ctx, &ms, "machines", "list", "-j", "--app", app); err != nil {
		return nil, err
	}
	return ms, nil
}

// waitMachinesHealthy polls until at least `want` machines are state=started
// with all their checks status=passing, or timeout/ctx fires.
func waitMachinesHealthy(ctx context.Context, app string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastSummary string
	for {
		ms, err := listMachines(ctx, app)
		if err != nil {
			log.Printf("listMachines: %v (will retry)", err)
		} else {
			healthy := 0
			for _, m := range ms {
				if m.State != "started" || len(m.Checks) == 0 {
					continue
				}
				ok := true
				for _, c := range m.Checks {
					if c.Status != "passing" {
						ok = false
						break
					}
				}
				if ok {
					healthy++
				}
			}
			summary := fmt.Sprintf("%d/%d started+passing (total machines: %d)", healthy, want, len(ms))
			if summary != lastSummary {
				log.Printf("status: %s", summary)
				lastSummary = summary
			}
			if healthy >= want {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for %d healthy machines", want)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// proxyGroup manages one or more long-running `fly proxy` subprocesses.
// `fly proxy` only accepts a single local:remote per invocation, so we run
// one process per port mapping and stop them all together.
type proxyGroup struct {
	cmds []*exec.Cmd
}

func startProxies(app string, ports []string) (*proxyGroup, error) {
	g := &proxyGroup{}
	for _, p := range ports {
		cmd := exec.Command("fly", "proxy", p, "--app", app, "--quiet")
		cmd.Stdout = io.Discard
		cmd.Stderr = os.Stderr
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			g.stop()
			return nil, fmt.Errorf("start proxy %s: %w", p, err)
		}
		g.cmds = append(g.cmds, cmd)
	}
	return g, nil
}

func (g *proxyGroup) stop() {
	if g == nil {
		return
	}
	for _, cmd := range g.cmds {
		if cmd.Process == nil {
			continue
		}
		pgid, err := syscall.Getpgid(cmd.Process.Pid)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			continue
		}
		_ = syscall.Kill(-pgid, syscall.SIGTERM)
		// Escalate to SIGKILL if `fly proxy` ignores SIGTERM — otherwise Wait
		// would block test teardown indefinitely.
		t := time.AfterFunc(5*time.Second, func() { _ = syscall.Kill(-pgid, syscall.SIGKILL) })
		_ = cmd.Wait()
		t.Stop()
	}
}
