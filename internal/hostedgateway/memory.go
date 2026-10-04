package hostedgateway

import (
	"errors"
	"sync"
)

type memoryBudget struct {
	mu   sync.Mutex
	used int64
}
type memorySpan struct {
	budget *memoryBudget
	used   int64
	done   bool
}

func (s *memorySpan) ReserveMemory(n int, _ uint8) error {
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	if n < 0 || s.done || int64(n) > 64*1024*1024-s.budget.used {
		return errors.New("gateway transport memory pressure")
	}
	s.budget.used += int64(n)
	s.used += int64(n)
	return nil
}
func (s *memorySpan) ReleaseMemory(n int) {
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	if n < 0 || int64(n) > s.used {
		panic("invalid hosted transport memory release")
	}
	s.used -= int64(n)
	s.budget.used -= int64(n)
}
func (s *memorySpan) Done() {
	s.budget.mu.Lock()
	defer s.budget.mu.Unlock()
	if !s.done {
		s.budget.used -= s.used
		s.used = 0
		s.done = true
	}
}
