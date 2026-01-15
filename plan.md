# LAPP - Log Auto Pattern Pipeline

## Vision

Transform a chaotic log stream into multiple semantic substreams, and continuously analyze their trends, anomalies, and pattern changes within a pipeline.

## Core Concept

### The Problem

Raw logs are messy. A single log stream contains many different types of events mixed together:
- User actions
- System events
- Errors and warnings
- Health checks
- Background jobs

Manually writing regex or parsers for each pattern is tedious and doesn't scale.

### The Solution

Use LLM to automatically discover patterns from log samples, then split the log stream into meaningful substreams for independent analysis.

## Pattern Discovery Flow

```
1. User pipes logs via stdin

2. Auto-discover (one-shot)
   - Sample first 50 lines → LLM
   - LLM returns: [{ regex: "...", description: "..." }, ...]
   - User reviews / adjusts

3. Apply regex pipeline
   - Match in order
   - Matched → corresponding substream
   - Unmatched → leftover

4. User inspects leftover
   - Too noisy? → Manually trigger another round
   - Acceptable? → Done
```

## LLM Output Format

```json
[
  {
    "regex": "\\[INFO\\] User \\d+ logged in",
    "description": "User login events"
  },
  {
    "regex": "\\[ERROR\\] Connection timeout to .*",
    "description": "Connection timeout errors"
  }
]
```

## Tech Stack

- **Runtime**: Bun
- **LLM SDK**: Vercel AI SDK
- **LLM Provider**: OpenRouter
- **Model**: google/gemini-2.0-flash-001

## Current Implementation (MVP)

- [x] Read logs from stdin (Unix pipe friendly)
- [x] LLM-based pattern discovery with structured output
- [x] Apply regex pipeline to split logs into substreams
- [x] Show leftover for manual inspection

## Future Work

### Phase 1: Core Enhancement
- [ ] Save/load pattern configurations
- [ ] Interactive mode for pattern refinement
- [ ] Better leftover analysis (trigger re-discovery)

### Phase 2: Substream Analysis
- [ ] Per-substream statistics (count, rate, trend)
- [ ] Anomaly detection within substreams
- [ ] Pattern lifecycle tracking (appear/disappear/change)

### Phase 3: Pipeline Visualization
- [ ] Express pipeline as a graph
- [ ] Web UI for pipeline management
- [ ] Real-time streaming support

## Design Principles

1. **Progressive**: Don't aim for perfect classification upfront. Iterate.
2. **LLM as eyes**: Let AI "see" logs and generate regex. Human reviews.
3. **Leftover as safety net**: Always have a bucket for unclassified logs.
4. **Controllable**: Support both manual and automatic modes.

## Usage

```bash
# Set API key
export OPENROUTER_API_KEY=your_key

# Discover patterns
cat logs.txt | bun run src/index.ts discover

# From kubectl
kubectl logs deploy/xxx | bun run src/index.ts discover

# Apply saved patterns
cat logs.txt | bun run src/index.ts apply patterns.json
```
