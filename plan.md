# LAPP Go Project Skeleton Plan

## Context

LAPP (Log Auto Pattern Pipeline) has completed research (29 papers) and architecture design, but has zero Go code. We need to set up the full project skeleton covering all modules in ARCHITECTURE.md, get the end-to-end pipeline working: CLI → Ingestor → Parser → Store → Querier.

## Key Decisions

- **Drain library**: `github.com/Jaeyo/go-drain3` as initial implementation, interface-based design allows future rewrite
- **Ingestor**: reads arbitrary log files (not Loghub-specific), Loghub only used in integration tests
- **Existing TS code**: Leave in place, no conflict with Go code

## Project Structure

```
lapp/
├── go.mod                          # module github.com/strrl/lapp
├── cmd/
│   └── lapp/
│       └── main.go                 # CLI entrypoint: ingest / query subcommands
├── pkg/
│   ├── ingestor/
│   │   ├── ingestor.go             # read log file → stream of LogEntry
│   │   └── ingestor_test.go
│   ├── parser/
│   │   ├── parser.go               # Parser interface, Template, Result types
│   │   ├── chain.go                # ChainParser: JSON → Grok → Drain → LLM, priority chain
│   │   ├── json.go                 # JSON log parser
│   │   ├── json_test.go
│   │   ├── grok.go                 # Grok pattern parser (basic patterns: syslog, nginx, etc.)
│   │   ├── grok_test.go
│   │   ├── drain.go                # DrainParser wrapping go-drain3
│   │   ├── drain_test.go
│   │   ├── llm.go                  # LLM extractor interface + stub (real impl later)
│   │   ├── llm_test.go
│   │   └── chain_test.go
│   ├── store/
│   │   ├── store.go                # Store interface
│   │   ├── duckdb.go               # DuckDB store: log entries, templates, labels, config
│   │   └── duckdb_test.go
│   ├── querier/
│   │   ├── querier.go              # query by template, time range, labels
│   │   └── querier_test.go
│   └── loghub/
│       ├── loader.go               # CSV loader for integration tests only
│       └── loader_test.go
└── integration_test.go             # end-to-end: load HDFS → full pipeline → query results
```

## Implementation Steps

### Step 1: Go module init + dependencies
- `go mod init github.com/strrl/lapp`
- Dependencies: go-drain3, go-duckdb, grok library

### Step 2: Ingestor (`pkg/ingestor/`)
- `ingestor.go`: `Ingest(filePath string) (chan LogEntry, error)` — read file line by line, emit LogEntry (raw line + line number + timestamp if parseable)
- Also support reading from stdin (filePath = "-")
- `ingestor_test.go`: test with a small temp file

### Step 3: Parser interface + all strategies (`pkg/parser/`)
- `parser.go`: define `Parser` interface, `Template`, `Result` types
- `json.go`: detect and parse JSON log lines, extract structured fields
- `grok.go`: match against predefined grok patterns (syslog, common log, etc.)
- `drain.go`: DrainParser wrapping go-drain3
- `llm.go`: LLMParser interface + stub implementation that always returns "miss" (real LLM integration later)
- `chain.go`: ChainParser executes strategies in priority order: JSON → Grok → Drain → LLM. First match wins.

### Step 4: Store (`pkg/store/`)
- `store.go`: define Store interface
- `duckdb.go`: DuckDB store — log entries, templates, labels, parser config, all in one DB. Auto-create tables on init.

### Step 5: Querier (`pkg/querier/`)
- `querier.go`: Querier wraps Store, provides high-level query methods:
  - `ByTemplate(templateID) []LogEntry`
  - `Summary() []TemplateSummary` (template + count)
  - `Search(opts QueryOpts) []LogEntry` (time range, label filter)

### Step 6: CLI (`cmd/lapp/main.go`)
- `ingest` subcommand: read log file → Parser chain → Store
- `query` subcommand: query stored logs by template/label
- `templates` subcommand: list discovered templates
- No cobra, just stdlib flag + os.Args dispatch

### Step 7: Loghub test helper (`pkg/loghub/`)
- `loader.go`: `LoadDataset(csvPath string) ([]LogEntry, error)` for integration tests
- `loader_test.go`: load HDFS 2k corrected CSV, verify entry count

### Step 8: Integration test (`integration_test.go`)
- Load HDFS 2k dataset → write to temp log file → run full pipeline (Ingestor → Parser → Store → Querier)
- Verify: templates discovered, entries stored, queries return correct results
- Skip if `LOGHUB_PATH` env var not set

## Verification

```bash
# All unit tests
go test ./...

# Loghub integration test
LOGHUB_PATH=/Users/strrl/playground/GitHub/.claude-playground/Loghub-2.0/2k_dataset go test -v -run TestIntegration

# Manual CLI test
go run ./cmd/lapp/ ingest /path/to/some.log
go run ./cmd/lapp/ templates
go run ./cmd/lapp/ query --template "E1"
```

## Files to Modify

- `CLAUDE.md` — add Go build/test commands
- `.gitignore` — add Go binary paths, *.db files

## Not in Scope (for this skeleton)

- Real LLM integration (stub only, interface ready)
- Multiline detection
- Benchmark metrics (GA, PA, FTA)
- Web UI
- Elasticsearch / GCP Logging ingestors
