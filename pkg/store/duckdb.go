package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	// DuckDB driver for database/sql.
	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/go-errors/errors"
)

var _ Store = (*DuckDBStore)(nil)

// DuckDBStore implements Store using DuckDB.
type DuckDBStore struct {
	db *sql.DB
}

// NewDuckDBStore creates a new DuckDB-backed store.
// Pass dsn="" for in-memory, or a file path for persistent storage.
func NewDuckDBStore(dsn string) (*DuckDBStore, error) {
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, errors.Errorf("open duckdb: %w", err)
	}
	return &DuckDBStore{db: db}, nil
}

// Init creates the log_entries and patterns tables if they do not exist.
func (s *DuckDBStore) Init(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE SEQUENCE IF NOT EXISTS log_entries_id_seq START 1`); err != nil {
		return errors.Errorf("create sequence: %w", err)
	}
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS log_entries (
			id BIGINT DEFAULT nextval('log_entries_id_seq'),
			line_number INTEGER,
			end_line_number INTEGER,
			timestamp TIMESTAMP,
			raw VARCHAR,
			pattern_id VARCHAR
		)
	`)
	if err != nil {
		return errors.Errorf("create log_entries table: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS patterns (
			pattern_id VARCHAR PRIMARY KEY,
			pattern_type VARCHAR,
			raw_pattern VARCHAR,
			semantic_id VARCHAR,
			description VARCHAR
		)
	`)
	if err != nil {
		return errors.Errorf("create patterns table: %w", err)
	}

	return nil
}

// InsertLog stores a single log entry.
func (s *DuckDBStore) InsertLog(ctx context.Context, entry LogEntry) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO log_entries (line_number, end_line_number, timestamp, raw, pattern_id)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.LineNumber,
		entry.EndLineNumber,
		entry.Timestamp,
		entry.Raw,
		entry.PatternUUIDString,
	)
	if err != nil {
		return errors.Errorf("insert log: %w", err)
	}
	return nil
}

// InsertLogBatch stores multiple log entries in a single transaction.
func (s *DuckDBStore) InsertLogBatch(ctx context.Context, entries []LogEntry) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO log_entries (line_number, end_line_number, timestamp, raw, pattern_id)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return errors.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entries {
		_, err = stmt.ExecContext(ctx, e.LineNumber, e.EndLineNumber, e.Timestamp, e.Raw, e.PatternUUIDString)
		if err != nil {
			return errors.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Errorf("commit: %w", err)
	}
	return nil
}

// QueryByPattern returns log entries matching the given pattern ID.
func (s *DuckDBStore) QueryByPattern(ctx context.Context, patternID string) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, line_number, end_line_number, timestamp, raw, pattern_id
		 FROM log_entries WHERE pattern_id = ?`,
		patternID,
	)
	if err != nil {
		return nil, errors.Errorf("query by pattern: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// QueryLogs returns log entries matching the given options.
func (s *DuckDBStore) QueryLogs(ctx context.Context, opts QueryOpts) ([]LogEntry, error) {
	var conditions []string
	var args []any

	if opts.PatternUUIDString != "" {
		conditions = append(conditions, "pattern_id = ?")
		args = append(args, opts.PatternUUIDString)
	}
	if !opts.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, opts.From)
	}
	if !opts.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, opts.To)
	}

	query := "SELECT id, line_number, end_line_number, timestamp, raw, pattern_id FROM log_entries"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY line_number"
	if opts.Limit > 0 {
		// DuckDB's database/sql driver does not reliably bind LIMIT via placeholder,
		// so we interpolate the int directly. This is safe as opts.Limit is an int.
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, errors.Errorf("query logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// PatternSummaries returns all patterns with their occurrence counts,
// joined with pattern metadata from the patterns table.
func (s *DuckDBStore) PatternSummaries(ctx context.Context) ([]PatternSummary, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT le.pattern_id, COALESCE(p.raw_pattern, ''), COUNT(*) as cnt,
		        COALESCE(p.pattern_type, ''), COALESCE(p.semantic_id, ''), COALESCE(p.description, '')
		 FROM log_entries le
		 INNER JOIN patterns p ON le.pattern_id = p.pattern_id
		 GROUP BY le.pattern_id, p.raw_pattern, p.pattern_type, p.semantic_id, p.description
		 ORDER BY cnt DESC`,
	)
	if err != nil {
		return nil, errors.Errorf("pattern summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []PatternSummary
	for rows.Next() {
		var ps PatternSummary
		if err := rows.Scan(&ps.PatternUUIDString, &ps.Pattern, &ps.Count, &ps.PatternType, &ps.SemanticID, &ps.Description); err != nil {
			return nil, errors.Errorf("scan summary: %w", err)
		}
		summaries = append(summaries, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Errorf("rows err: %w", err)
	}
	return summaries, nil
}

// InsertPatterns upserts patterns into the patterns table.
func (s *DuckDBStore) InsertPatterns(ctx context.Context, patterns []Pattern) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// ON CONFLICT: update only structural fields; preserve semantic_id and description
	// set by 'lapp label' so that re-ingestion does not wipe LLM-generated labels.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO patterns (pattern_id, pattern_type, raw_pattern, semantic_id, description)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(pattern_id) DO UPDATE SET
		     pattern_type = excluded.pattern_type,
		     raw_pattern  = excluded.raw_pattern`,
	)
	if err != nil {
		return errors.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, p := range patterns {
		_, err = stmt.ExecContext(ctx, p.PatternUUIDString, p.PatternType, p.RawPattern, p.SemanticID, p.Description)
		if err != nil {
			return errors.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Errorf("commit: %w", err)
	}
	return nil
}

// Patterns returns all patterns from the patterns table.
func (s *DuckDBStore) Patterns(ctx context.Context) ([]Pattern, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pattern_id, pattern_type, raw_pattern,
		        COALESCE(semantic_id, ''), COALESCE(description, '')
		 FROM patterns
		 ORDER BY pattern_id`,
	)
	if err != nil {
		return nil, errors.Errorf("query patterns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var patterns []Pattern
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.PatternUUIDString, &p.PatternType, &p.RawPattern, &p.SemanticID, &p.Description); err != nil {
			return nil, errors.Errorf("scan pattern: %w", err)
		}
		patterns = append(patterns, p)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Errorf("rows err: %w", err)
	}
	return patterns, nil
}

// UpdatePatternLabels updates only semantic_id and description for the given patterns.
func (s *DuckDBStore) UpdatePatternLabels(ctx context.Context, labels []Pattern) error {
	if len(labels) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return errors.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE patterns SET semantic_id = ?, description = ? WHERE pattern_id = ?`,
	)
	if err != nil {
		return errors.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, l := range labels {
		_, err = stmt.ExecContext(ctx, l.SemanticID, l.Description, l.PatternUUIDString)
		if err != nil {
			return errors.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Errorf("commit: %w", err)
	}
	return nil
}

// ClearOrphanPatternIDs sets pattern_id to empty for log entries
// whose pattern_id does not exist in the patterns table.
func (s *DuckDBStore) ClearOrphanPatternIDs(ctx context.Context) (int64, error) {
	result, err := s.db.ExecContext(ctx,
		`UPDATE log_entries SET pattern_id = ''
		 WHERE pattern_id != '' AND pattern_id NOT IN (SELECT pattern_id FROM patterns)`,
	)
	if err != nil {
		return 0, errors.Errorf("clear orphan pattern IDs: %w", err)
	}
	return result.RowsAffected()
}

// PatternCounts returns the number of log entries per pattern_id.
func (s *DuckDBStore) PatternCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT pattern_id, COUNT(*) FROM log_entries WHERE pattern_id != '' GROUP BY pattern_id`,
	)
	if err != nil {
		return nil, errors.Errorf("pattern counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var id string
		var cnt int
		if err := rows.Scan(&id, &cnt); err != nil {
			return nil, errors.Errorf("scan: %w", err)
		}
		counts[id] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Errorf("rows err: %w", err)
	}
	return counts, nil
}

// Close closes the underlying database connection.
func (s *DuckDBStore) Close() error {
	return s.db.Close()
}

func scanEntries(rows *sql.Rows) ([]LogEntry, error) {
	var entries []LogEntry
	for rows.Next() {
		var e LogEntry
		var ts time.Time
		if err := rows.Scan(&e.ID, &e.LineNumber, &e.EndLineNumber, &ts, &e.Raw, &e.PatternUUIDString); err != nil {
			return nil, errors.Errorf("scan entry: %w", err)
		}
		e.Timestamp = ts
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Errorf("rows err: %w", err)
	}
	return entries, nil
}
