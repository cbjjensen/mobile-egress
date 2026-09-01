package tailscale

import (
	"errors"
	"io"
	"sync"
)

var errTailscaleCommandOutputLimit = errors.New("Tailscale command output limit exceeded")

type tailscaleCommandOutputBudget struct {
	mu         sync.Mutex
	remaining  int
	exceeded   bool
	cancel     func()
	cancelOnce sync.Once
}

func newTailscaleCommandOutputBudget(limit int, cancel func()) *tailscaleCommandOutputBudget {
	return &tailscaleCommandOutputBudget{remaining: limit, cancel: cancel}
}

func (budget *tailscaleCommandOutputBudget) Writer(sink io.Writer, observe func([]byte)) io.Writer {
	if sink == nil {
		sink = io.Discard
	}
	return tailscaleCommandOutputWriter{budget: budget, sink: sink, observe: observe}
}

func (budget *tailscaleCommandOutputBudget) Exceeded() bool {
	budget.mu.Lock()
	defer budget.mu.Unlock()
	return budget.exceeded
}

type tailscaleCommandOutputWriter struct {
	budget  *tailscaleCommandOutputBudget
	sink    io.Writer
	observe func([]byte)
}

func (writer tailscaleCommandOutputWriter) Write(chunk []byte) (int, error) {
	budget := writer.budget
	budget.mu.Lock()
	if budget.exceeded {
		budget.mu.Unlock()
		return 0, errTailscaleCommandOutputLimit
	}

	allowed := len(chunk)
	if allowed > budget.remaining {
		allowed = budget.remaining
		budget.exceeded = true
	}
	written, err := writer.sink.Write(chunk[:allowed])
	budget.remaining -= written
	if err == nil && written != allowed {
		err = io.ErrShortWrite
	}
	if written > 0 && writer.observe != nil {
		writer.observe(append([]byte(nil), chunk[:written]...))
	}
	overflow := budget.exceeded
	budget.mu.Unlock()

	if overflow {
		budget.cancelOnce.Do(func() {
			if budget.cancel != nil {
				budget.cancel()
			}
		})
		return written, errTailscaleCommandOutputLimit
	}
	return written, err
}
