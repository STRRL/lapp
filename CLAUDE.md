# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) is a tool that automatically discovers log templates from log streams using multi-strategy parsing (JSON, Grok, Drain, LLM) and stores structured results in DuckDB for querying.

## Go Commands

```bash
# Build
go build ./cmd/lapp/

# Run all unit tests
go test ./pkg/...

# Run integration test (requires Loghub-2.0 dataset)
LOGHUB_PATH=/path/to/Loghub-2.0/2k_dataset go test -v -run TestIntegration .

# CLI usage
go run ./cmd/lapp/ ingest <logfile> [--db <path>]
go run ./cmd/lapp/ templates [--db <path>]
go run ./cmd/lapp/ query --template <id> [--db <path>]

# Read from stdin
cat logs.txt | go run ./cmd/lapp/ ingest - --db lapp.duckdb
```

## Architecture

See `ARCHITECTURE.md` for full module design. Key modules:

```
cmd/lapp/           CLI entrypoint
pkg/ingestor/       Read log files → stream of LogLine
pkg/parser/         Multi-strategy parser chain: JSON → Grok → Drain → LLM
pkg/store/          DuckDB storage for log entries and templates
pkg/querier/        Query layer over store
pkg/loghub/         Loghub-2.0 CSV loader (integration tests only)
```

**Parser Chain:** JSON → Grok → Drain → LLM (first match wins)

**Data Flow:** CLI → Ingestor → Parser Chain → Store → Querier

## Tech Stack

- Language: Go
- Parser: go-drain3, trivago/grok
- Storage: DuckDB (via duckdb-go/v2)
- LLM: stub for now (interface ready)
