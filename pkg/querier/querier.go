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

// ByTemplate returns log entries matching the given template ID.
func (q *Querier) ByTemplate(templateID string) ([]store.LogEntry, error) {
	return q.store.QueryByTemplate(templateID)
}

// Summary returns all templates with their occurrence counts.
func (q *Querier) Summary() ([]store.TemplateSummary, error) {
	return q.store.TemplateSummaries()
}

// Search returns log entries matching the given query options.
func (q *Querier) Search(opts store.QueryOpts) ([]store.LogEntry, error) {
	return q.store.QueryLogs(opts)
}
