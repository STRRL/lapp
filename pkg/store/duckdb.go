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

// Init creates the log_entries table if it does not exist.
func (s *DuckDBStore) Init() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS log_entries (
			id BIGINT DEFAULT nextval('log_entries_id_seq'),
			line_number INTEGER,
			timestamp TIMESTAMP,
			raw VARCHAR,
			template_id VARCHAR,
			template VARCHAR
		)
	`)
	if err != nil {
		// Try creating the sequence first if it doesn't exist
		_, _ = s.db.Exec(`CREATE SEQUENCE IF NOT EXISTS log_entries_id_seq START 1`)
		_, err = s.db.Exec(`
			CREATE TABLE IF NOT EXISTS log_entries (
				id BIGINT DEFAULT nextval('log_entries_id_seq'),
				line_number INTEGER,
				timestamp TIMESTAMP,
				raw VARCHAR,
				template_id VARCHAR,
				template VARCHAR
			)
		`)
		if err != nil {
			return fmt.Errorf("create table: %w", err)
		}
	}
	return nil
}

// InsertLog stores a single log entry.
func (s *DuckDBStore) InsertLog(entry LogEntry) error {
	_, err := s.db.Exec(
		`INSERT INTO log_entries (line_number, timestamp, raw, template_id, template)
		 VALUES (?, ?, ?, ?, ?)`,
		entry.LineNumber,
		entry.Timestamp,
		entry.Raw,
		entry.TemplateID,
		entry.Template,
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
		`INSERT INTO log_entries (line_number, timestamp, raw, template_id, template)
		 VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	for _, e := range entries {
		_, err = stmt.Exec(e.LineNumber, e.Timestamp, e.Raw, e.TemplateID, e.Template)
		if err != nil {
			return fmt.Errorf("exec: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// QueryByTemplate returns log entries matching the given template ID.
func (s *DuckDBStore) QueryByTemplate(templateID string) ([]LogEntry, error) {
	rows, err := s.db.Query(
		`SELECT id, line_number, timestamp, raw, template_id, template
		 FROM log_entries WHERE template_id = ?`,
		templateID,
	)
	if err != nil {
		return nil, fmt.Errorf("query by template: %w", err)
	}
	defer func() { _ = rows.Close() }()
	return scanEntries(rows)
}

// QueryLogs returns log entries matching the given options.
func (s *DuckDBStore) QueryLogs(opts QueryOpts) ([]LogEntry, error) {
	var conditions []string
	var args []any

	if opts.TemplateID != "" {
		conditions = append(conditions, "template_id = ?")
		args = append(args, opts.TemplateID)
	}
	if !opts.From.IsZero() {
		conditions = append(conditions, "timestamp >= ?")
		args = append(args, opts.From)
	}
	if !opts.To.IsZero() {
		conditions = append(conditions, "timestamp <= ?")
		args = append(args, opts.To)
	}

	query := "SELECT id, line_number, timestamp, raw, template_id, template FROM log_entries"
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

// TemplateSummaries returns all templates with their occurrence counts.
func (s *DuckDBStore) TemplateSummaries() ([]TemplateSummary, error) {
	rows, err := s.db.Query(
		`SELECT template_id, template, COUNT(*) as cnt
		 FROM log_entries
		 GROUP BY template_id, template
		 ORDER BY cnt DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("template summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var summaries []TemplateSummary
	for rows.Next() {
		var ts TemplateSummary
		if err := rows.Scan(&ts.TemplateID, &ts.Template, &ts.Count); err != nil {
			return nil, fmt.Errorf("scan summary: %w", err)
		}
		summaries = append(summaries, ts)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows err: %w", err)
	}
	return summaries, nil
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
		if err := rows.Scan(&e.ID, &e.LineNumber, &ts, &e.Raw, &e.TemplateID, &e.Template); err != nil {
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
