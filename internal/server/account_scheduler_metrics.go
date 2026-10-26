package server

import "github.com/prometheus/client_golang/prometheus"

// accountSchedulerCollector reports process-local scheduler state. Reasons are
// fixed categories supplied by the account managers, never upstream text.
type accountSchedulerCollector struct {
	accounts AccountProviders
	checks   *prometheus.Desc
	times    *prometheus.Desc
}

func newAccountSchedulerCollector(accounts AccountProviders) *accountSchedulerCollector {
	return &accountSchedulerCollector{
		accounts: accounts,
		checks:   prometheus.NewDesc("llm_proxy_account_scheduler_checks_total", "Automatic account checks by sanitized reason; resets on process restart.", []string{"provider", "reason"}, nil),
		times:    prometheus.NewDesc("llm_proxy_account_scheduler_last_timestamp_seconds", "Last completed check, mutation attempt, or successful acknowledgment, as a Unix timestamp; absent if never observed on this process.", []string{"provider", "event"}, nil),
	}
}

func (c *accountSchedulerCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.checks
	ch <- c.times
}

func (c *accountSchedulerCollector) Collect(ch chan<- prometheus.Metric) {
	emit := func(provider string, counts map[string]uint64, checked, attempted, success int64) {
		for reason, count := range counts {
			ch <- prometheus.MustNewConstMetric(c.checks, prometheus.CounterValue, float64(count), provider, reason)
		}
		for event, timestamp := range map[string]int64{"check": checked, "attempt": attempted, "success": success} {
			if timestamp > 0 {
				ch <- prometheus.MustNewConstMetric(c.times, prometheus.GaugeValue, float64(timestamp), provider, event)
			}
		}
	}
	if c.accounts.ZCode != nil {
		s := c.accounts.ZCode.AutoClaimStatus()
		emit("zcode", s.ReasonCounts, s.LastCheckUnix, s.LastAttemptUnix, s.LastSuccessUnix)
	}
	if c.accounts.MiniMaxCode != nil {
		s := c.accounts.MiniMaxCode.AutoCheckinStatus()
		emit("minimax-code", s.ReasonCounts, s.LastCheckUnix, s.LastAttemptUnix, s.LastSuccessUnix)
	}
}
