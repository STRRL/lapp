# Event Schema v1

`lapp` treats every parsed log record as a normalized event with three concerns kept separate:

- `text`: the raw source line that came in
- `attrs`: structured attributes parsed directly from the source
- `inferred`: metadata synthesized after parsing, such as a generalized pattern or owning entity

This keeps ingestion lossless while giving downstream steps a stable shape to work with even when log formats vary.

## Canonical Shape

```json
{
  "ts": "2026-03-10T21:00:00Z",
  "text": "ts=2026-03-10T21:00:00Z level=info service=auth-api request_id=req_123 msg=\"user user_456 authenticated\"",
  "attrs": {
    "level": "info",
    "service": "auth-api",
    "request_id": "req_123",
    "msg": "user user_456 authenticated"
  },
  "inferred": {
    "pattern": "user <*> authenticated",
    "entity": "auth-api"
  }
}
```

## Top-Level Fields

| Field | Type | Required | Notes |
|---|---|---|---|
| `ts` | RFC3339 timestamp string | No | Optional because plain text logs may not expose a trustworthy timestamp. |
| `text` | string | Yes | Raw log line, preserved verbatim as the source of truth. |
| `attrs` | object of string to string | Yes | Parsed key/value attributes extracted directly from the log line. Use `{}` when nothing can be extracted. |
| `inferred` | object | Yes | Metadata derived from parsing or later enrichment. Use `{}` when nothing is inferred yet. |

## Parsed Attributes

`attrs` stays intentionally flat in v1. Values are strings so the schema remains stable across JSON, logfmt, `key=value`, and plain text sources.

Recommended canonical keys when they can be recovered confidently:

| Key | Required | Meaning |
|---|---|---|
| `level` | No | Severity such as `debug`, `info`, `warn`, or `error`. |
| `service` | No | Service, worker, or subsystem name. |
| `env` | No | Deployment environment such as `prod` or `staging`. |
| `request_id` | No | Request-scoped identifier. |
| `trace_id` | No | Distributed trace identifier. |
| `span_id` | No | Distributed tracing span identifier. |
| `correlation_id` | No | Cross-system correlation token when `request_id` is not the right semantic fit. |
| `user_id` | No | User identifier present in the source line. |
| `endpoint` | No | HTTP or RPC target when available. |
| `method` | No | HTTP or RPC verb when available. |

Additional keys are allowed when they represent source fields that are useful to preserve.

## Inferred Metadata

`inferred` is reserved for values that are not copied verbatim from the source.

| Key | Required | Meaning |
|---|---|---|
| `pattern` | No | Generalized event template such as `user <*> authenticated`. |
| `entity` | No | Owning component, domain object, or actor inferred from context. |

## Fixture Coverage

Representative fixtures live in `fixtures/events/v1/`:

- `json-checkout-failure.json`
- `logfmt-auth-success.json`
- `key-value-retry.json`
- `plain-text-worker-stall.json`

Each fixture wraps a normalized event with `name`, `source_format`, and `description` metadata so future parser and schema tests can consume them directly.
