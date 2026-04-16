// Package scheduler wraps robfig/cron to run the periodic version-check job.
package scheduler

import (
	"context"
	"log/slog"

	"github.com/robfig/cron/v3"
)

// CheckFunc is the function called on each scheduled tick.
type CheckFunc func(ctx context.Context)

// Scheduler wraps a cron runner with a cancellable context.
type Scheduler struct {
	c        *cron.Cron
	checkFn  CheckFunc
	interval string
	log      *slog.Logger
}

// New creates a Scheduler. interval must be a valid cron expression,
// e.g. "*/30 * * * *" for every 30 minutes.
func New(interval string, fn CheckFunc, log *slog.Logger) *Scheduler {
	return &Scheduler{
		c: cron.New(
			cron.WithLogger(cron.PrintfLogger(slogAdapter{log})),
		),
		checkFn:  fn,
		interval: interval,
		log:      log,
	}
}

// Start registers the job and begins the scheduler. It is non-blocking.
func (s *Scheduler) Start(ctx context.Context) error {
	_, err := s.c.AddFunc(s.interval, func() {
		s.log.Info("running scheduled version check")
		s.checkFn(ctx)
	})
	if err != nil {
		return err
	}
	s.c.Start()
	s.log.Info("scheduler started", "interval", s.interval)
	return nil
}

// Stop gracefully stops the scheduler and waits for any running jobs to finish.
func (s *Scheduler) Stop() {
	ctx := s.c.Stop()
	<-ctx.Done()
}

// slogAdapter bridges cron's Printf logger to slog.
type slogAdapter struct{ log *slog.Logger }

func (a slogAdapter) Printf(format string, v ...any) {
	a.log.Debug("cron: "+format, v...)
}
