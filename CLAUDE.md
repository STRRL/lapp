# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LAPP (Log Auto Pattern Pipeline) is a CLI tool that uses LLM to automatically discover regex patterns from log streams and split them into semantic substreams. It reads logs from stdin, uses Gemini via OpenRouter to identify patterns, and applies regex matching to categorize logs.

## Commands

```bash
# Install dependencies
bun install

# Discover patterns from logs
cat logs.txt | bun run src/index.ts discover
kubectl logs deploy/xxx | bun run src/index.ts discover

# Apply saved patterns to logs
cat logs.txt | bun run src/index.ts apply patterns.json

# Or use pnpm scripts
pnpm start        # defaults to discover
pnpm discover     # explicit discover
pnpm apply        # apply (requires patterns.json arg)
```

## Environment Setup

Requires `OPENROUTER_API_KEY` environment variable. See `.env.example`.

## Architecture

```
src/
├── index.ts      # CLI entrypoint - reads stdin, dispatches commands
├── types.ts      # Zod schemas for Pattern and DiscoverResult
├── discover.ts   # LLM integration - calls OpenRouter/Gemini for pattern discovery
└── pipeline.ts   # Regex matching logic - applies patterns to log lines
```

**Data Flow:**
1. `index.ts` reads log lines from stdin
2. `discover` command: samples first 50 lines → LLM generates patterns → applies pipeline → outputs results
3. `apply` command: loads patterns from JSON file → applies pipeline → outputs results

**Key Types:**
- `Pattern`: `{ regex: string, description: string }`
- `PipelineResult`: `{ streams: Map<description, lines[]>, leftover: string[] }`

## Tech Stack

- Runtime: Bun
- LLM: Vercel AI SDK with OpenRouter (google/gemini-2.0-flash-001)
- Schema validation: Zod (for structured LLM output)
