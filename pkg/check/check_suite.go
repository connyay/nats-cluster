package check

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

type OnCompletionHook func()
type CheckSuite struct {
	Name          string
	Checks        []*Check
	OnCompletion  OnCompletionHook
	ErrOnSetup    error
	executionTime time.Duration
	processed     bool
	clean         bool
}

func NewCheckSuite(name string) *CheckSuite {
	return &CheckSuite{Name: name}
}

func (h *CheckSuite) Process(parentCtx context.Context) {
	ctx, cancel := context.WithCancel(parentCtx)
	start := time.Now()
	for _, check := range h.Checks {
		check.Process()
	}
	h.executionTime = RoundDuration(time.Since(start), 2)
	h.processed = true
	h.runOnCompletion()
	cancel()

	select {
	case <-ctx.Done():
		// Handle timeout
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			h.executionTime = RoundDuration(time.Since(start), 2)
			h.processed = true
			h.runOnCompletion()
		}
	}
}

func (h *CheckSuite) runOnCompletion() {
	if h.clean {
		return
	}
	if h.OnCompletion != nil {
		h.OnCompletion()
	}
	h.clean = true
}

func (h *CheckSuite) AddCheck(name string, checkFunc CheckFunction) *Check {
	check := &Check{Name: name, CheckFunc: checkFunc}
	h.Checks = append(h.Checks, check)
	return check
}

func (h *CheckSuite) Passed() bool {
	for _, check := range h.Checks {
		if !check.Passed() {
			return false
		}
	}
	return true
}

func (h *CheckSuite) Result() string {
	checkStr := []string{}
	for _, check := range h.Checks {
		checkStr = append(checkStr, check.Result())
	}
	return strings.Join(checkStr, "\n")
}

func (h *CheckSuite) RawResult() string {
	checkStr := []string{}
	for _, check := range h.Checks {
		checkStr = append(checkStr, check.RawResult())
	}
	return strings.Join(checkStr, "\n")
}

// Print will send output straight to stdout.
func (h *CheckSuite) Print() {
	if h.processed {
		for _, check := range h.Checks {
			slog.Info(check.Result())
		}
		slog.Info("Check suite completed",
			"suite", h.Name,
			"execution_time", h.executionTime)
	} else {
		if len(h.Checks) > 0 {
			slog.Info("Check suite not processed",
				"suite", h.Name,
				"pending_checks", len(h.Checks))
		} else {
			slog.Info("Check suite has no checks to evaluate",
				"suite", h.Name)
		}
	}
}
