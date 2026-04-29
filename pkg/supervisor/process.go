package supervisor

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

type cmdFactory func() *exec.Cmd

type process struct {
	name         string
	color        int
	output       *multiOutput
	stopSignal   os.Signal
	restart      bool
	restartDelay time.Duration
	maxRestarts  int

	f   cmdFactory
	dir string
	env []string

	mu      sync.Mutex
	cmd     *exec.Cmd
	running bool
}

type Opt func(*process)

func WithEnv(env map[string]string) Opt {
	return func(proc *process) {
		for k, v := range env {
			proc.env = append(proc.env, fmt.Sprintf("%s=%s", k, v))
		}
	}
}

func WithStopSignal(sig os.Signal) Opt {
	return func(proc *process) {
		proc.stopSignal = sig
	}
}

func WithRootDir(dir string) Opt {
	return func(proc *process) {
		proc.dir = dir
	}
}

// WithRestart restarts the process if it exists. If limit
// is 0 it will restart forever.
func WithRestart(limit int, delay time.Duration) Opt {
	return func(proc *process) {
		proc.restart = true
		proc.maxRestarts = limit
		proc.restartDelay = delay
	}
}

func (p *process) writeLine(b []byte) {
	p.output.WriteLine(p, b)
}

func (p *process) writeErr(err error) {
	p.output.WriteErr(p, err)
}

func (p *process) signal(sig os.Signal) {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return
	}
	group, err := os.FindProcess(-cmd.Process.Pid)
	if err != nil {
		p.writeErr(err)
		return
	}

	if err = group.Signal(sig); err != nil {
		p.writeErr(err)
	}
}

func (p *process) Running() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.running && p.cmd != nil && p.cmd.Process != nil
}

func (p *process) Run() {
	cmd := p.f()
	p.mu.Lock()
	p.cmd = cmd
	p.running = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.cmd = nil
		p.running = false
		p.mu.Unlock()
	}()

	p.output.PipeOutput(p)
	defer p.output.ClosePipe(p)

	ensureKill(cmd)

	p.writeLine([]byte("\033[1mRunning...\033[0m"))

	if err := cmd.Run(); err != nil {
		p.writeErr(err)
	} else {
		status := cmd.ProcessState.ExitCode()
		p.writeLine([]byte(fmt.Sprintf("\033[1mProcess exited %d\033[0m", status)))
	}
}

func (p *process) Interrupt() {
	if p.Running() {
		p.writeLine([]byte(fmt.Sprintf("\033[1mStopping %s...\033[0m", p.stopSignal)))
		p.signal(p.stopSignal)
	}
}

func (p *process) Kill() {
	if p.Running() {
		p.writeLine([]byte("\033[1mKilling...\033[0m"))
		p.signal(syscall.SIGKILL)
	}
}
