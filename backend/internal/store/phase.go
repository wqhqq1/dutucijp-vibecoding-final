package store

import (
	"context"
	"time"
)

type PhaseTiming struct {
	Phase   string  `json:"phase"`
	Seconds float64 `json:"seconds"`
}

type phaseObserverKey struct{}

// WithPhaseObserver supplies optional diagnostic timing only. Observation and
// all production work stay inside the existing Import/Run measurement boundary.
func WithPhaseObserver(ctx context.Context, observer func(PhaseTiming)) context.Context {
	return context.WithValue(ctx, phaseObserverKey{}, observer)
}

func observePhase(ctx context.Context, phase string, started time.Time) {
	if observer, ok := ctx.Value(phaseObserverKey{}).(func(PhaseTiming)); ok && observer != nil {
		observer(PhaseTiming{Phase: phase, Seconds: time.Since(started).Seconds()})
	}
}
