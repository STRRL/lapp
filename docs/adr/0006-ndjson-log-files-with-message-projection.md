# Structured logs are stored as NDJSON and discovered via message projection

Log files can be NDJSON (one JSON entry per line) as well as plain text, detected per file by the pipeline — the capability belongs to the pipeline, not to any import provider, so hand-uploaded NDJSON files get the same treatment as imported ones. Imported GCP entries land in a fixed envelope: `{"ts": ..., "severity": ..., "payload": {...}}` with the jsonPayload nested untouched (textPayload becomes `payload.message`); provider metadata such as labels, trace, and resource stays in the ImportRun record, not in the log lines.

For pattern discovery, JSON entries are projected to a text line rather than fed to Drain whole: the first string field among `payload.message`, `msg`, `log`, `error` becomes the projection (`<severity> <message>`); if none exists, the compact payload JSON is the fallback. Timestamps are excluded from the projection to keep Drain templates clean.

**Considered Options**

- Flatten everything to text at import time (structure lost)
- NDJSON storage, discovery on compact-JSON lines (structure kept, dirty templates)
- NDJSON storage with message projection (chosen)
- JSON-native pattern discovery by structure/key-set (a second discovery engine — deferred, not rejected)

**Consequences**

The envelope and the projection rule are part of the workspace file contract (ADR 0001). Payload fields are never flattened to the top level, so user fields named `severity` or `ts` cannot collide with the envelope. Analysis agents can query structure with jq-style tools instead of grepping flattened text. If real usage shows dirty templates for payloads without a message-like field, the projection rule is the extension point.
