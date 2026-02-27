package querier

import (
	"context"

	"github.com/strrl/lapp/pkg/store"
)

// Querier provides a high-level interface for querying log entries.
type Querier struct {
	store store.Store
}

// NewQuerier creates a new Querier backed by the given store.
func NewQuerier(s store.Store) *Querier {
	return &Querier{store: s}
}

// ByPattern returns log entries matching the given pattern ID.
func (q *Querier) ByPattern(ctx context.Context, patternID string) ([]store.LogEntry, error) {
	return q.store.QueryByPattern(ctx, patternID)
}

// Summary returns all patterns with their occurrence counts.
func (q *Querier) Summary(ctx context.Context) ([]store.PatternSummary, error) {
	return q.store.PatternSummaries(ctx)
}

// Search returns log entries matching the given query options.
func (q *Querier) Search(ctx context.Context, opts store.QueryOpts) ([]store.LogEntry, error) {
	return q.store.QueryLogs(ctx, opts)
}
