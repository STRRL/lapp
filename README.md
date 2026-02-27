# LAPP — Log Auto Pattern Pipeline

AI-native log pattern discovery: cluster logs cheaply, semantify templates with LLM, view structured results.

## Quick Start

```bash
# Build
go build ./cmd/lapp/

# Ingest logs (stdin or file)
cat app.log | go run ./cmd/lapp/ ingest - --db lapp.duckdb
go run ./cmd/lapp/ ingest /var/log/syslog --db lapp.duckdb

# View discovered templates
go run ./cmd/lapp/ templates --db lapp.duckdb

# Query logs by template
go run ./cmd/lapp/ query --template D1 --db lapp.duckdb

# AI-powered analysis (requires OPENROUTER_API_KEY)
go run ./cmd/lapp/ analyze app.log "why are there connection timeouts?"
```

## How It Works

```
Log File/stdin
  │
  ▼
Ingestor (streaming)
  │
  ▼
Parser Chain (first match wins)
  ├─ JSONParser   → detects JSON, extracts message/keys
  ├─ GrokParser   → SYSLOG, Apache common/combined
  └─ DrainParser  → online clustering (go-drain3)
  │
  ▼
DuckDB Store (log_entries: line_number, raw, template_id, template)
  │
  ▼
Query / Analyze
```

**Core idea**: Drain clusters logs into templates cheaply (no API cost), then LLM semantifies the templates in a single call. This follows the IBM "Label Broadcasting" pattern — cluster first (90%+ volume reduction), apply LLM to representatives, broadcast labels back.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for `analyze` and `debug run` commands
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- `.env` file is auto-loaded

## Commands

| Command | Description |
|---|---|
| `ingest <file>` | Parse log file, store in DuckDB |
| `templates` | Show discovered templates with counts |
| `query --template <id>` | Filter logs by template ID |
| `analyze <file> [question]` | AI-powered log analysis |
| `debug workspace <file>` | Build analysis workspace without LLM |
| `debug run <dir> [question]` | Run AI agent on existing workspace |

## Development

```bash
go test ./pkg/...                    # Unit tests
LOGHUB_PATH=/path/to/2k_dataset \
  go test -v -run TestIntegration .  # Integration tests (14 Loghub-2.0 datasets)
```

## Roadmap

See [Issue #2](https://github.com/STRRL/lapp/issues/2) for full vision and progress.

### Current (v0.2) — Parser Pipeline ✅

Multi-strategy parser chain (JSON → Grok → Drain), DuckDB storage, query layer, agentic analyzer, integration tests across 14 Loghub-2.0 datasets.

### Next: LLM Semantic Labeling

Give every Drain template a human-readable semantic ID and description via a single LLM call. Drain produces `D1: "Starting <*> on port <*>"` → LLM labels it `server-startup: "Server process starting on a specific port"`.

### Next: Log Viewer

Color-coded log viewer with template filtering. Each semantic template gets a distinct color. Unmatched/leftover logs shown in gray.

### Future

- Iterative refinement (re-discover patterns from leftover)
- Per-template statistics and trend detection
- Real-time streaming, pipeline-as-config
- MCP server for LLM agent access

## License

MIT
