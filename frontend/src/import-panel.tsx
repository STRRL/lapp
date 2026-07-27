import { timestampDate } from "@bufbuild/protobuf/wkt";
import { AlertTriangle, CloudDownload, History } from "lucide-react";
import { useState } from "react";
import { ImportRun, ImportRunState } from "./gen/lapp/web/v1/web_pb";
import { useAppStore } from "./store";

export function ImportPanel({ hasActiveRun }: { hasActiveRun: boolean }) {
  const importRuns = useAppStore((state) => state.importRuns);
  const recentImportQueries = useAppStore((state) => state.recentImportQueries);
  const busy = useAppStore((state) => state.busy);
  const runAction = useAppStore((state) => state.runAction);
  const startImport = useAppStore((state) => state.startImport);
  const [project, setProject] = useState("");
  const [filter, setFilter] = useState("");
  const [range, setRange] = useState(() => rangeForHours(1));
  const [selectedHours, setSelectedHours] = useState<number | null>(1);
  const [limit, setLimit] = useState("100000");
  const latestRun = importRuns[0];
  const disabled = busy || hasActiveRun;

  const useQuickRange = (hours: number) => {
    setSelectedHours(hours);
    setRange(rangeForHours(hours));
  };

  const submit = async () => {
    const from = new Date(range.from);
    const to = new Date(range.to);
    const parsedLimit = Number(limit);
    if (!project.trim()) {
      throw new Error("Project is required.");
    }
    if (Number.isNaN(from.getTime()) || Number.isNaN(to.getTime())) {
      throw new Error("From and to are required.");
    }
    if (from > to) {
      throw new Error("From must not be after to.");
    }
    if (!Number.isInteger(parsedLimit) || parsedLimit < 1) {
      throw new Error("Limit must be a positive whole number.");
    }

    await startImport({
      project: project.trim(),
      filter,
      from,
      to,
      limit: parsedLimit
    });
  };

  return (
    <section className="import-view">
      <div className="import-top">
        <form
          className="panel import-form"
          onSubmit={(event) => {
            event.preventDefault();
            void runAction(submit);
          }}
        >
          <header className="panel-heading">
            <div>
              <h2>Import GCP logs</h2>
              <p>The server uses local Application Default Credentials.</p>
            </div>
            <CloudDownload size={22} />
          </header>

          <label>
            <span>Recent query</span>
            <select
              value=""
              disabled={disabled || recentImportQueries.length === 0}
              onChange={(event) => {
                const query = recentImportQueries[Number(event.target.value)];
                if (!query) return;
                setProject(query.project);
                setFilter(query.filter);
              }}
            >
              <option value="">Choose a past query</option>
              {recentImportQueries.map((query, index) => (
                <option key={`${query.project}\u0000${query.filter}`} value={index}>{recentQueryLabel(query.project, query.filter)}</option>
              ))}
            </select>
          </label>

          <div className="form-grid">
            <label>
              <span>Project</span>
              <input
                value={project}
                required
                disabled={disabled}
                placeholder="my-gcp-project"
                onChange={(event) => setProject(event.target.value)}
              />
            </label>
            <label>
              <span>Limit</span>
              <input
                type="number"
                min="1"
                step="1"
                value={limit}
                required
                disabled={disabled}
                onChange={(event) => setLimit(event.target.value)}
              />
            </label>
          </div>

          <label>
            <span>Filter</span>
            <input
              value={filter}
              disabled={disabled}
              placeholder='severity >= ERROR'
              onChange={(event) => setFilter(event.target.value)}
            />
          </label>

          <fieldset disabled={disabled}>
            <legend>Time range</legend>
            <div className="quick-ranges">
              {[1, 6, 24].map((hours) => (
                <button
                  key={hours}
                  type="button"
                  className={selectedHours === hours ? "selected" : ""}
                  onClick={() => useQuickRange(hours)}
                >
                  Last {hours}h
                </button>
              ))}
            </div>
            <div className="form-grid">
              <label>
                <span>From</span>
                <input
                  type="datetime-local"
                  value={range.from}
                  required
                  onChange={(event) => {
                    setSelectedHours(null);
                    setRange((current) => ({ ...current, from: event.target.value }));
                  }}
                />
              </label>
              <label>
                <span>To</span>
                <input
                  type="datetime-local"
                  value={range.to}
                  required
                  onChange={(event) => {
                    setSelectedHours(null);
                    setRange((current) => ({ ...current, to: event.target.value }));
                  }}
                />
              </label>
            </div>
          </fieldset>

          <div className="form-actions">
            <button type="submit" disabled={disabled || !project.trim()}>
              <CloudDownload size={17} />
              Start Import
            </button>
          </div>
        </form>

        <section className={latestRun?.state === ImportRunState.FAILED ? "panel import-latest failed" : "panel import-latest"}>
          <header className="panel-heading">
            <div>
              <h2>Latest import</h2>
              <p>Current state and final result.</p>
            </div>
            <History size={22} />
          </header>
          {latestRun ? (
            <>
              <div className="import-state-line">
                <StateBadge state={latestRun.state} />
                <span>{formatTimestamp(latestRun.startedAt)}</span>
              </div>
              <p className="import-message">{importRunMessage(latestRun)}</p>
              <dl>
                <dt>Project</dt>
                <dd>{latestRun.project}</dd>
                <dt>Range</dt>
                <dd>{formatRange(latestRun)}</dd>
                <dt>Limit</dt>
                <dd>{latestRun.limit.toLocaleString()}</dd>
              </dl>
              {latestRun.truncated && (
                <div className="truncation-warning">
                  <AlertTriangle size={17} />
                  Results reached the limit and were truncated.
                </div>
              )}
            </>
          ) : (
            <p className="empty-copy">No imports for this workspace.</p>
          )}
        </section>
      </div>

      <section className="panel import-history">
        <header className="panel-heading">
          <div>
            <h2>Import history</h2>
            <p>Newest runs appear first.</p>
          </div>
        </header>
        {importRuns.length > 0 ? (
          <div className="table-scroll">
            <table>
              <thead>
                <tr>
                  <th>Started</th>
                  <th>Project and filter</th>
                  <th>Range</th>
                  <th>State</th>
                  <th>Entries</th>
                  <th>File</th>
                </tr>
              </thead>
              <tbody>
                {importRuns.map((run) => (
                  <tr key={run.name}>
                    <td>{formatTimestamp(run.startedAt)}</td>
                    <td>
                      <strong>{run.project}</strong>
                      <code className="filter-value">{run.filter || "All logs"}</code>
                    </td>
                    <td>{formatRange(run)}</td>
                    <td>
                      <StateBadge state={run.state} />
                      {run.error && (
                        <span className="run-error">
                          {run.error.code}: {run.error.message}
                        </span>
                      )}
                    </td>
                    <td>
                      {run.state === ImportRunState.SUCCEEDED ? run.entryCount.toLocaleString() : ""}
                      {run.truncated && <span className="small-warning">Truncated</span>}
                    </td>
                    <td>{run.logFileName}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        ) : (
          <p className="empty-copy">No import history.</p>
        )}
      </section>
    </section>
  );
}

function StateBadge({ state }: { state: ImportRunState }) {
  return <span className={`state-badge ${importStateClass(state)}`}>{importStateLabel(state)}</span>;
}

function importRunMessage(run: ImportRun) {
  switch (run.state) {
    case ImportRunState.QUEUED:
      return "Waiting to start.";
    case ImportRunState.RUNNING:
      return "Reading matching log entries from GCP.";
    case ImportRunState.SUCCEEDED:
      return `${run.entryCount.toLocaleString()} entries written to ${run.logFileName}. Start discovery when ready.`;
    case ImportRunState.FAILED:
      return run.error ? `${run.error.code}: ${run.error.message}` : "Import failed without an error message.";
    default:
      return "Waiting for state.";
  }
}

function importStateLabel(state: ImportRunState) {
  return ImportRunState[state]?.toLowerCase() ?? "unknown";
}

function importStateClass(state: ImportRunState) {
  switch (state) {
    case ImportRunState.SUCCEEDED:
      return "succeeded";
    case ImportRunState.FAILED:
      return "failed";
    case ImportRunState.QUEUED:
    case ImportRunState.RUNNING:
      return "running";
    default:
      return "";
  }
}

function recentQueryLabel(project: string, filter: string) {
  return filter ? `${project} · ${filter}` : `${project} · All logs`;
}

function formatRange(run: ImportRun) {
  return `${formatTimestamp(run.from)} to ${formatTimestamp(run.to)}`;
}

function formatTimestamp(value: ImportRun["from"]) {
  return value ? timestampDate(value).toLocaleString() : "";
}

function rangeForHours(hours: number) {
  const to = new Date();
  const from = new Date(to.getTime() - hours * 60 * 60 * 1000);
  return {
    from: datetimeLocalValue(from),
    to: datetimeLocalValue(to)
  };
}

function datetimeLocalValue(value: Date) {
  const local = new Date(value.getTime() - value.getTimezoneOffset() * 60 * 1000);
  return local.toISOString().slice(0, 16);
}
