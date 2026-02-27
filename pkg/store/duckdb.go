package store

import (
	"context"
	"database/sql"
	"encoding/json"
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
			labels JSON
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

func marshalLabels(labels map[string]string) (string, error) {
	if labels == nil {
		return "{}", nil
	}
	b, err := json.Marshal(labels)
	if err != nil {
		return "", errors.Errorf("marshal labels: %w", err)
	}
	return string(b), nil
}

// InsertLog stores a single log entry.
func (s *DuckDBStore) InsertLog(ctx context.Context, entry LogEntry) error {
	labelsJSON, err := marshalLabels(entry.Labels)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO log_entries (line_number, end_line_number, timestamp, raw, labels)
		 VALUES (?, ?, ?, ?, ?::JSON)`,
		entry.LineNumber,
		entry.EndLineNumber,
		entry.Timestamp,
		entry.Raw,
		labelsJSON,
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
		`INSERT INTO log_entries (line_number, end_line_number, timestamp, raw, labels)
		 VALUES (?, ?, ?, ?, ?::JSON)`,
	)
	if err != nil {
		return errors.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entries {
		labelsJSON, err := marshalLabels(e.Labels)
		if err != nil {
			return err
		}
		_, err = stmt.ExecContext(ctx, e.LineNumber, e.EndLineNumber, e.Timestamp, e.Raw, labelsJSON)
		if err != nil {
			return errors.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return errors.Errorf("commit: %w", err)
	}
	return nil
}

// QueryByPattern returns log entries matching the given pattern semantic ID via labels.
func (s *DuckDBStore) QueryByPattern(ctx context.Context, pattern string) ([]LogEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, line_number, end_line_number, timestamp, raw, CAST(labels AS VARCHAR)
		 FROM log_entries WHERE json_extract_string(labels, '$.pattern') = ?`,
		pattern,
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

	if opts.Pattern != "" {
		conditions = append(conditions, "json_extract_string(labels, '$.pattern') = ?")
		args = append(args, opts.Pattern)
	}
	if !opts.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, opts.From)
	}
	if !opts.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, opts.To)
	}

	query := "SELECT id, line_number, end_line_number, timestamp, raw, CAST(labels AS VARCHAR) FROM log_entries"
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
		`SELECT p.pattern_id, COALESCE(p.raw_pattern, ''), COUNT(*) as cnt,
		        COALESCE(p.pattern_type, ''), COALESCE(p.semantic_id, ''), COALESCE(p.description, '')
		 FROM log_entries le
		 INNER JOIN patterns p ON json_extract_string(le.labels, '$.pattern') = p.semantic_id
		 GROUP BY p.pattern_id, p.raw_pattern, p.pattern_type, p.semantic_id, p.description
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

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO patterns (pattern_id, pattern_type, raw_pattern, semantic_id, description)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(pattern_id) DO UPDATE SET
		     pattern_type = excluded.pattern_type,
		     raw_pattern  = excluded.raw_pattern,
		     semantic_id  = excluded.semantic_id,
		     description  = excluded.description`,
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

// PatternCounts returns the number of log entries per pattern semantic ID.
func (s *DuckDBStore) PatternCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT json_extract_string(labels, '$.pattern'), COUNT(*)
		 FROM log_entries
		 WHERE json_extract_string(labels, '$.pattern') IS NOT NULL AND json_extract_string(labels, '$.pattern') != ''
		 GROUP BY json_extract_string(labels, '$.pattern')`,
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

// InternalDB returns the underlying *sql.DB for direct SQL queries.
func (s *DuckDBStore) InternalDB() *sql.DB {
	return s.db
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
		var labelsJSON string
		if err := rows.Scan(&e.ID, &e.LineNumber, &e.EndLineNumber, &ts, &e.Raw, &labelsJSON); err != nil {
			return nil, errors.Errorf("scan entry: %w", err)
		}
		e.Timestamp = ts
		if labelsJSON != "" {
			if err := json.Unmarshal([]byte(labelsJSON), &e.Labels); err != nil {
				return nil, errors.Errorf("unmarshal labels: %w", err)
			}
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.Errorf("rows err: %w", err)
	}
	return entries, nil
}
