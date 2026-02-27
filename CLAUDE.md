# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) is a tool that automatically discovers log templates from log streams using multi-strategy parsing (JSON, Drain) and stores structured results in DuckDB for querying. It also includes an agentic analyzer that uses LLMs to investigate logs.

## Go Commands

```bash
# Build
go build ./cmd/lapp/

# Run all unit tests
go test ./pkg/...

# Run a single test
go test -v -run TestFunctionName ./pkg/parser/

# Run integration test (requires Loghub-2.0 dataset)
LOGHUB_PATH=/path/to/Loghub-2.0/2k_dataset go test -v -run TestIntegration .

# CLI usage
go run ./cmd/lapp/ ingest <logfile> [--db <path>]
go run ./cmd/lapp/ templates [--db <path>]
go run ./cmd/lapp/ query --template <id> [--db <path>]
go run ./cmd/lapp/ analyze <logfile> [question]

# Debug subcommands (build workspace without LLM, or run agent on existing workspace)
go run ./cmd/lapp/ debug workspace <logfile> [-o <dir>]
go run ./cmd/lapp/ debug run <workspace-dir> [question] [--model <model>]

# Read from stdin
cat logs.txt | go run ./cmd/lapp/ ingest - --db lapp.duckdb
```

## Architecture

See `ARCHITECTURE.md` for full module design. Key modules:

```
cmd/lapp/           CLI entrypoint (cobra commands)
pkg/ingestor/       Read log files → stream of LogLine
pkg/parser/         Multi-strategy parser chain: JSON → Drain
pkg/store/          DuckDB storage for log entries and templates
pkg/querier/        Query layer over store
pkg/analyzer/       Agentic log analysis: builds workspace files, runs LLM agent via eino ADK
pkg/test/loghub/    Loghub-2.0 CSV loader (integration tests only)
```

**Parser Chain:** JSON → Drain (first match wins, via `ChainParser`)

**Data Flow:** CLI → Ingestor → Parser Chain → Store → Querier

**Analyzer Flow:** Logs → Parser Chain → Workspace (raw.log, summary.txt, errors.txt) → eino ADK agent with filesystem tools → analysis result

## Key Interfaces

- `parser.Parser` (`pkg/parser/parser.go`): All parsers implement `Parse(content string) Result` and `Templates() []Template`
- `store.Store` (`pkg/store/store.go`): Storage interface with `Init`, `InsertLog`, `InsertLogBatch`, `QueryByTemplate`, etc.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for the `analyze` and `debug run` commands (LLM-powered analysis)
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- `.env` file is auto-loaded via godotenv

## Tech Stack

- Language: Go
- CLI: cobra
- Parser: go-drain3
- Storage: DuckDB (via duckdb-go/v2)
- LLM Agent: cloudwego/eino ADK + OpenRouter API

## Code Style

- `nolint` directives should be placed on the line above the target, not as end-of-line comments
