# LAPP — Log Auto Pattern Pipeline

AI-native log pattern discovery: cluster logs cheaply, semantify templates with LLM, view structured results.

## Quick Start

```bash
# Build
go build ./cmd/lapp/

# Create a workspace
go run ./cmd/lapp/ workspace create app-incident

# Add logs to the workspace (semantic labeling via OpenRouter)
go run ./cmd/lapp/ workspace add-log --topic app-incident /var/log/syslog

# AI-powered analysis (agent backend via ACP provider)
go run ./cmd/lapp/ workspace analyze --topic app-incident "why are there connection timeouts?" --acp claude
go run ./cmd/lapp/ workspace analyze --topic app-incident "what failed?" --acp codex
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
Workspace Notes / Analyze
```

**Core idea**: Drain clusters logs into templates cheaply (no API cost), then LLM semantifies the templates in a single call. This follows the IBM "Label Broadcasting" pattern — cluster first (90%+ volume reduction), apply LLM to representatives, broadcast labels back.

## Environment Variables

- `OPENROUTER_API_KEY`: Required for semantic labeling in `workspace add-log`
- `MODEL_NAME`: Override default LLM model (default: `google/gemini-3-flash-preview`)
- Provider-specific auth for ACP agent CLI (for example Claude/Codex/Gemini CLI login credentials)
- `.env` file is auto-loaded

## Commands

| Command | Description |
|---|---|
| `workspace create <topic>` | Create a workspace under `~/.lapp/workspaces/` |
| `workspace list` | List all workspace topics |
| `workspace add-log --topic <topic> <file>` | Add log file and rebuild patterns/notes |
| `workspace analyze --topic <topic> [question]` | Run AI analysis (`--acp claude|codex|gemini`) |

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
