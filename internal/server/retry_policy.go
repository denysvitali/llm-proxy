package server

import "time"

// retryBudget is the retry policy for one backend, resolved from config with
// defaults filled in.
type retryBudget struct {
	attempts   int           // extra connection-phase attempts after a transient failure
	maxBackoff time.Duration // cap on any single retry pause
}

func (s *Server) retryBudgetFor(backendName string) retryBudget {
	b := retryBudget{attempts: defaultRetryAttempts, maxBackoff: defaultRetryMaxBackoff}
	if bc, ok := s.cfg.BackendByType(backendName); ok {
		if bc.RetryAttempts > 0 {
			b.attempts = bc.RetryAttempts
		}
		if bc.RetryMaxBackoff > 0 {
			b.maxBackoff = bc.RetryMaxBackoff
		}
	}
	return b
}
