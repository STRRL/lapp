package querier

import (
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
func (q *Querier) ByPattern(patternID string) ([]store.LogEntry, error) {
	return q.store.QueryByPattern(patternID)
}

// Summary returns all patterns with their occurrence counts.
func (q *Querier) Summary() ([]store.PatternSummary, error) {
	return q.store.PatternSummaries()
}

// Search returns log entries matching the given query options.
func (q *Querier) Search(opts store.QueryOpts) ([]store.LogEntry, error) {
	return q.store.QueryLogs(opts)
}
