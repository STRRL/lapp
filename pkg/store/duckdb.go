package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"
)

// DuckDBStore implements Store using DuckDB.
type DuckDBStore struct {
	db *sql.DB
}

// NewDuckDBStore creates a new DuckDB-backed store.
// Pass dsn="" for in-memory, or a file path for persistent storage.
func NewDuckDBStore(dsn string) (*DuckDBStore, error) {
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	return &DuckDBStore{db: db}, nil
}

// Init creates the log_entries and patterns tables if they do not exist,
// and migrates old schemas from previous versions.
func (s *DuckDBStore) Init() error {
	if err := s.migrateLogEntries(); err != nil {
		return err
	}

	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS patterns (
			pattern_id VARCHAR PRIMARY KEY,
			pattern_type VARCHAR,
			raw_pattern VARCHAR,
			semantic_id VARCHAR,
			description VARCHAR
		)
	`)
	if err != nil {
		return fmt.Errorf("create patterns table: %w", err)
	}

	return nil
}

// migrateLogEntries ensures the log_entries table exists with the current schema.
// If an old table with template_id/template columns exists, it is migrated.
func (s *DuckDBStore) migrateLogEntries() error {
	// Check if the table exists at all.
	var tableExists bool
	err := s.db.QueryRow(`
		SELECT COUNT(*) > 0 FROM information_schema.tables
		WHERE table_name = 'log_entries'
	`).Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("check table existence: %w", err)
	}

	if !tableExists {
		_, _ = s.db.Exec(`CREATE SEQUENCE IF NOT EXISTS log_entries_id_seq START 1`)
		_, err := s.db.Exec(`
			CREATE TABLE log_entries (
				id BIGINT DEFAULT nextval('log_entries_id_seq'),
				line_number INTEGER,
				timestamp TIMESTAMP,
				raw VARCHAR,
				pattern_id VARCHAR
			)
		`)
		if err != nil {
			return fmt.Errorf("create log_entries table: %w", err)
		}
		return nil
	}

	// Table exists — check if it has the old schema (template_id column).
	var hasOldColumn bool
	err = s.db.QueryRow(`
		SELECT COUNT(*) > 0 FROM information_schema.columns
		WHERE table_name = 'log_entries' AND column_name = 'template_id'
	`).Scan(&hasOldColumn)
	if err != nil {
		return fmt.Errorf("check old schema: %w", err)
	}

	if hasOldColumn {
		// Migrate: rename template_id → pattern_id, drop template column.
		_, err = s.db.Exec(`ALTER TABLE log_entries RENAME COLUMN template_id TO pattern_id`)
		if err != nil {
			return fmt.Errorf("rename template_id to pattern_id: %w", err)
		}
		// The old schema had a 'template' column — drop it if present.
		_, _ = s.db.Exec(`ALTER TABLE log_entries DROP COLUMN template`)
	}

	return nil
}

// InsertLog stores a single log entry.
func (s *DuckDBStore) InsertLog(entry LogEntry) error {
	_, err := s.db.Exec(
		`INSERT INTO log_entries (line_number, timestamp, raw, pattern_id)
		 VALUES (?, ?, ?, ?)`,
		entry.LineNumber,
		entry.Timestamp,
		entry.Raw,
		entry.PatternID,
	)
	if err != nil {
		return fmt.Errorf("insert log: %w", err)
	}
	return nil
}

// InsertLogBatch stores multiple log entries in a single transaction.
func (s *DuckDBStore) InsertLogBatch(entries []LogEntry) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		`INSERT INTO log_entries (line_number, timestamp, raw, pattern_id)
		 VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entries {
		_, err = stmt.Exec(e.LineNumber, e.Timestamp, e.Raw, e.PatternID)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// QueryByPattern returns log entries matching the given pattern ID.
func (s *DuckDBStore) QueryByPattern(patternID string) ([]LogEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, line_number, timestamp, raw, pattern_id
		 FROM log_entries WHERE pattern_id = ?`,
		patternID,
	)
	if err != nil {
		return nil, fmt.Errorf("query by pattern: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// QueryLogs returns log entries matching the given options.
func (s *DuckDBStore) QueryLogs(opts QueryOpts) ([]LogEntry, error) {
	var conditions []string
	var args []any

	if opts.PatternID != "" {
		conditions = append(conditions, "pattern_id = ?")
		args = append(args, opts.PatternID)
	}
	if !opts.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, opts.From)
	}
	if !opts.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, opts.To)
	}

	query := "SELECT id, line_number, timestamp, raw, pattern_id FROM log_entries"
	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY line_number"
	if opts.Limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", opts.Limit)
	}

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query logs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// PatternSummaries returns all patterns with their occurrence counts,
// joined with pattern metadata from the patterns table.
func (s *DuckDBStore) PatternSummaries() ([]PatternSummary, error) {
	rows, err := s.db.Query(
		`SELECT le.pattern_id, COALESCE(p.raw_pattern, ''), COUNT(*) as cnt,
		        COALESCE(p.pattern_type, ''), COALESCE(p.semantic_id, ''), COALESCE(p.description, '')
		 FROM log_entries le
		 LEFT JOIN patterns p ON le.pattern_id = p.pattern_id
		 GROUP BY le.pattern_id, p.raw_pattern, p.pattern_type, p.semantic_id, p.description
		 ORDER BY cnt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("pattern summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []PatternSummary
	for rows.Next() {
		var ps PatternSummary
		if err := rows.Scan(&ps.PatternID, &ps.Pattern, &ps.Count, &ps.PatternType, &ps.SemanticID, &ps.Description); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		summaries = append(summaries, ps)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return summaries, nil
}

// InsertPatterns upserts patterns into the patterns table.
func (s *DuckDBStore) InsertPatterns(patterns []Pattern) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		`INSERT OR REPLACE INTO patterns (pattern_id, pattern_type, raw_pattern, semantic_id, description)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, p := range patterns {
		_, err = stmt.Exec(p.PatternID, p.PatternType, p.RawPattern, p.SemanticID, p.Description)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// Patterns returns all patterns from the patterns table.
func (s *DuckDBStore) Patterns() ([]Pattern, error) {
	rows, err := s.db.Query(
		`SELECT pattern_id, pattern_type, raw_pattern,
		        COALESCE(semantic_id, ''), COALESCE(description, '')
		 FROM patterns
		 ORDER BY pattern_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("query patterns: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var patterns []Pattern
	for rows.Next() {
		var p Pattern
		if err := rows.Scan(&p.PatternID, &p.PatternType, &p.RawPattern, &p.SemanticID, &p.Description); err != nil {
			return nil, fmt.Errorf("scan pattern: %w", err)
		}
		patterns = append(patterns, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return patterns, nil
}

// UpdatePatternLabels updates only semantic_id and description for the given patterns.
func (s *DuckDBStore) UpdatePatternLabels(labels []Pattern) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.Prepare(
		`UPDATE patterns SET semantic_id = ?, description = ? WHERE pattern_id = ?`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, l := range labels {
		_, err = stmt.Exec(l.SemanticID, l.Description, l.PatternID)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// PatternCounts returns the number of log entries per pattern_id.
func (s *DuckDBStore) PatternCounts() (map[string]int, error) {
	rows, err := s.db.Query(
		`SELECT pattern_id, COUNT(*) FROM log_entries GROUP BY pattern_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("pattern counts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	counts := make(map[string]int)
	for rows.Next() {
		var id string
		var cnt int
		if err := rows.Scan(&id, &cnt); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		counts[id] = cnt
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
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
		if err := rows.Scan(&e.ID, &e.LineNumber, &ts, &e.Raw, &e.PatternID); err != nil {
			return nil, fmt.Errorf("scan entry: %w", err)
		}
		e.Timestamp = ts
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return entries, nil
}
