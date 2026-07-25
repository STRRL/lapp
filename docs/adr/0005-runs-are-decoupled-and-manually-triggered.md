# Runs are decoupled and manually triggered

ImportRun and DiscoveryRun are independent Runs in a workspace; no Run ever triggers another. Adding logs (upload or import) never starts discovery — the person explicitly starts a DiscoveryRun when they are done gathering logs. This reverses the original add-log behavior, which automatically started a DiscoveryRun after copying the file.

**Considered Options**

- Auto-trigger with coalescing (import completion enqueues discovery unless one is pending)
- Auto-trigger with a per-action opt-out flag
- Manual only

**Consequences**

Investigations that gather several log batches (multiple imports, multiple uploads, or a mix) run discovery once instead of once per file. The CLI gains an explicit `workspace discover` command and `add-log` becomes a pure copy. Web UI needs an explicit "run discovery" affordance, and a freshly-added log file can sit undiscovered until the user asks — that staleness is accepted as the price of a single, predictable rule.
