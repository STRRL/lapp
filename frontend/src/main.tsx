import React, { useEffect, useMemo, useRef } from "react";
import { createRoot } from "react-dom/client";
import {
  AlertTriangle,
  CloudDownload,
  FileText,
  FolderOpen,
  Play,
  Plus,
  RefreshCw,
  Trash2,
  Upload
} from "lucide-react";
import {
  DiscoveryRun,
  DiscoveryRunState,
  DiscoveryStep,
  ImportRunState,
  Pattern,
  WorkspaceStatus
} from "./gen/lapp/web/v1/web_pb";
import { ImportPanel } from "./import-panel";
import { useAppStore } from "./store";
import "./styles.css";

function App() {
  const workspaces = useAppStore((state) => state.workspaces);
  const selectedWorkspaceName = useAppStore((state) => state.selectedWorkspaceName);
  const logFiles = useAppStore((state) => state.logFiles);
  const importRuns = useAppStore((state) => state.importRuns);
  const discoveryRuns = useAppStore((state) => state.discoveryRuns);
  const activeOperationName = useAppStore((state) => state.activeOperationName);
  const selectedRunName = useAppStore((state) => state.selectedRunName);
  const selectedPatternName = useAppStore((state) => state.selectedPatternName);
  const patterns = useAppStore((state) => state.patterns);
  const errorPatterns = useAppStore((state) => state.errorPatterns);
  const unmatchedErrors = useAppStore((state) => state.unmatchedErrors);
  const activeTab = useAppStore((state) => state.activeTab);
  const newWorkspaceID = useAppStore((state) => state.newWorkspaceID);
  const notice = useAppStore((state) => state.notice);
  const busy = useAppStore((state) => state.busy);
  const setActiveTab = useAppStore((state) => state.setActiveTab);
  const setNewWorkspaceID = useAppStore((state) => state.setNewWorkspaceID);
  const selectWorkspace = useAppStore((state) => state.selectWorkspace);
  const selectRun = useAppStore((state) => state.selectRun);
  const selectPattern = useAppStore((state) => state.selectPattern);
  const runAction = useAppStore((state) => state.runAction);
  const refreshWorkspaces = useAppStore((state) => state.refreshWorkspaces);
  const refreshWorkspaceDetails = useAppStore((state) => state.refreshWorkspaceDetails);
  const refreshRecentImportQueries = useAppStore((state) => state.refreshRecentImportQueries);
  const refreshActiveOperation = useAppStore((state) => state.refreshActiveOperation);
  const refreshResults = useAppStore((state) => state.refreshResults);
  const createWorkspace = useAppStore((state) => state.createWorkspace);
  const deleteWorkspace = useAppStore((state) => state.deleteWorkspace);
  const uploadFiles = useAppStore((state) => state.uploadFiles);
  const deleteLogFile = useAppStore((state) => state.deleteLogFile);
  const startDiscovery = useAppStore((state) => state.startDiscovery);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const selectedWorkspace = useMemo(
    () => workspaces.find((workspace) => workspace.name === selectedWorkspaceName),
    [selectedWorkspaceName, workspaces]
  );
  const selectedRun = useMemo(
    () => discoveryRuns.find((run) => run.name === selectedRunName),
    [discoveryRuns, selectedRunName]
  );
  const selectedPattern = useMemo(
    () => patterns.find((pattern) => pattern.name === selectedPatternName),
    [patterns, selectedPatternName]
  );
  const isDiscovering = selectedWorkspace?.status === WorkspaceStatus.DISCOVERING;
  const isImporting = importRuns.some(
    (run) => run.state === ImportRunState.QUEUED || run.state === ImportRunState.RUNNING
  );
  const hasActiveRun = Boolean(activeOperationName) || isDiscovering || isImporting;

  useEffect(() => {
    void runAction(async () => {
      await Promise.all([refreshWorkspaces(), refreshRecentImportQueries()]);
    });
  }, [refreshRecentImportQueries, refreshWorkspaces, runAction]);

  useEffect(() => {
    void refreshWorkspaceDetails().catch((error: unknown) => {
      void runAction(async () => {
        throw error;
      });
    });
  }, [refreshWorkspaceDetails, selectedWorkspaceName, runAction]);

  useEffect(() => {
    void refreshResults().catch((error: unknown) => {
      void runAction(async () => {
        throw error;
      });
    });
  }, [refreshResults, selectedRunName, runAction]);

  useEffect(() => {
    if (!activeOperationName) {
      return;
    }
    const timer = window.setInterval(() => {
      void refreshActiveOperation().catch((error: unknown) => {
        void runAction(async () => {
          throw error;
        });
      });
    }, 1500);
    return () => window.clearInterval(timer);
  }, [activeOperationName, refreshActiveOperation, runAction]);

  return (
    <main className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-mark">L</span>
          <span>LAPP</span>
        </div>
        <form
          className="create-form"
          onSubmit={(event) => {
            event.preventDefault();
            void runAction(createWorkspace);
          }}
        >
          <input
            value={newWorkspaceID}
            onChange={(event) => setNewWorkspaceID(event.target.value)}
            placeholder="workspace name"
            disabled={busy}
          />
          <button type="submit" className="icon-button" disabled={busy || !newWorkspaceID.trim()} title="Create workspace">
            <Plus size={18} />
          </button>
        </form>
        <nav className="workspace-list">
          {workspaces.map((workspace) => (
            <button
              key={workspace.name}
              className={workspace.name === selectedWorkspaceName ? "workspace-item selected" : "workspace-item"}
              onClick={() => selectWorkspace(workspace.name)}
            >
              <FolderOpen size={18} />
              <span>
                <strong>{workspace.workspaceId}</strong>
                <small>{workspaceStatusLabel(workspace.status)}</small>
              </span>
            </button>
          ))}
        </nav>
      </aside>

      <section className="content">
        {selectedWorkspace ? (
          <>
            <header className="workspace-header">
              <div>
                <h1>{selectedWorkspace.workspaceId}</h1>
                <div className="metrics">
                  <span>{selectedWorkspace.logFileCount} logs</span>
                  <span>{selectedWorkspace.patternCount} patterns</span>
                  <span>{workspaceStatusLabel(selectedWorkspace.status)}</span>
                </div>
              </div>
              <div className="header-actions">
                <button onClick={() => void runAction(startDiscovery)} disabled={busy || hasActiveRun || logFiles.length === 0}>
                  <Play size={17} />
                  Start Discovery
                </button>
                <button
                  className="danger"
                  onClick={() => void runAction(() => deleteWorkspace(selectedWorkspace))}
                  disabled={busy || hasActiveRun}
                >
                  <Trash2 size={17} />
                  Delete
                </button>
              </div>
            </header>

            {notice && <div className="notice">{notice}</div>}

            <section className={selectedRun?.state === DiscoveryRunState.FAILED ? "run-strip failed" : "run-strip"}>
              <div>
                <strong>{selectedRun ? discoveryStateLabel(selectedRun.state) : "No successful discovery"}</strong>
                <span>{discoveryRunMessage(selectedRun)}</span>
              </div>
              <select value={selectedRunName} onChange={(event) => selectRun(event.target.value)}>
                <option value="">No run selected</option>
                {discoveryRuns.map((run) => (
                  <option key={run.name} value={run.name}>
                    {run.discoveryRunId.slice(0, 8)} · {discoveryStateLabel(run.state)}
                  </option>
                ))}
              </select>
            </section>

            <div className="tabs">
              <button className={activeTab === "logs" ? "active" : ""} onClick={() => setActiveTab("logs")}>
                <FileText size={16} />
                Logs
              </button>
              <button className={activeTab === "imports" ? "active" : ""} onClick={() => setActiveTab("imports")}>
                <CloudDownload size={16} />
                Imports
              </button>
              <button className={activeTab === "patterns" ? "active" : ""} onClick={() => setActiveTab("patterns")}>
                <RefreshCw size={16} />
                Patterns
              </button>
              <button className={activeTab === "errors" ? "active" : ""} onClick={() => setActiveTab("errors")}>
                <AlertTriangle size={16} />
                Errors
              </button>
            </div>

            {activeTab === "logs" && (
              <section className="panel">
                <div className="panel-toolbar">
                  <input
                    ref={fileInputRef}
                    className="file-input"
                    type="file"
                    multiple
                    disabled={busy || hasActiveRun}
                    onChange={(event) => {
                      void runAction(() => uploadFiles(event.target.files)).then(() => {
                        if (fileInputRef.current) {
                          fileInputRef.current.value = "";
                        }
                      });
                    }}
                  />
                  <button onClick={() => fileInputRef.current?.click()} disabled={busy || hasActiveRun}>
                    <Upload size={17} />
                    Upload
                  </button>
                </div>
                <Table
                  headers={["File", "Size", "Updated", ""]}
                  rows={logFiles.map((file) => [
                    file.fileName,
                    formatBytes(file.sizeBytes),
                    formatTimestamp(file.updatedAt),
                    <button
                      key={file.name}
                      className="icon-button danger"
                      onClick={() => void runAction(() => deleteLogFile(file))}
                      disabled={busy || hasActiveRun}
                      title="Delete log file"
                    >
                      <Trash2 size={16} />
                    </button>
                  ])}
                />
              </section>
            )}

            {activeTab === "imports" && (
              <ImportPanel key={selectedWorkspaceName} hasActiveRun={hasActiveRun} />
            )}

            {activeTab === "patterns" && (
              <section className="pattern-layout">
                <div className="panel pattern-grid">
                  {patterns.map((pattern) => (
                    <PatternRow
                      key={pattern.name}
                      pattern={pattern}
                      selected={pattern.name === selectedPatternName}
                      onOpen={() => selectPattern(pattern.name)}
                    />
                  ))}
                </div>
                {selectedPattern && <PatternDetail pattern={selectedPattern} />}
              </section>
            )}

            {activeTab === "errors" && (
              <section className="panel pattern-grid">
                {errorPatterns.map((pattern) => (
                  <PatternRow key={pattern.name} pattern={pattern} error />
                ))}
                {unmatchedErrors.map((line) => (
                  <article key={`${line.fileName}:${line.lineNumber}`} className="pattern-row error-row">
                    <div>
                      <h2>{line.fileName}:{line.lineNumber}</h2>
                      <code>{line.content}</code>
                    </div>
                  </article>
                ))}
              </section>
            )}
          </>
        ) : (
          <div className="empty-state">Create or select a workspace.</div>
        )}
      </section>
    </main>
  );
}

function PatternRow({
  pattern,
  error = false,
  selected = false,
  onOpen
}: {
  pattern: Pattern;
  error?: boolean;
  selected?: boolean;
  onOpen?: () => void;
}) {
  return (
    <article className={[error ? "pattern-row error-row" : "pattern-row", selected ? "selected" : ""].filter(Boolean).join(" ")}>
      <div>
        <h2>{pattern.semanticId}</h2>
        <p>{pattern.description}</p>
        <code>{pattern.template}</code>
      </div>
      <aside>
        <strong>{pattern.count}</strong>
        <span>matches</span>
        {onOpen && <button onClick={onOpen}>Open</button>}
      </aside>
      {!error && <pre>{pattern.samples.slice(0, 4).join("\n")}</pre>}
    </article>
  );
}

function PatternDetail({ pattern }: { pattern: Pattern }) {
  return (
    <aside className="pattern-detail">
      <header>
        <h2>{pattern.semanticId}</h2>
        <span>{pattern.count} matches</span>
      </header>
      <p>{pattern.description}</p>
      <section>
        <h3>Template</h3>
        <code>{pattern.template}</code>
      </section>
      <section>
        <h3>Samples</h3>
        <pre>{pattern.samples.join("\n") || "No samples"}</pre>
      </section>
      <section>
        <h3>Line refs</h3>
        <ul>
          {pattern.lineRefs.slice(0, 50).map((ref, index) => (
            <li key={`${ref.fileName}:${ref.lineNumber}:${index}`}>
              {ref.fileName}:{ref.lineNumber}
            </li>
          ))}
        </ul>
      </section>
    </aside>
  );
}

function Table({ headers, rows }: { headers: string[]; rows: Array<Array<React.ReactNode>> }) {
  return (
    <table>
      <thead>
        <tr>{headers.map((header) => <th key={header}>{header}</th>)}</tr>
      </thead>
      <tbody>
        {rows.map((row, index) => (
          <tr key={index}>
            {row.map((cell, cellIndex) => <td key={cellIndex}>{cell}</td>)}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function workspaceStatusLabel(status: WorkspaceStatus) {
  return WorkspaceStatus[status]?.toLowerCase() ?? "unknown";
}

function discoveryStateLabel(state: DiscoveryRunState) {
  return DiscoveryRunState[state]?.toLowerCase() ?? "unknown";
}

function discoveryRunMessage(run: DiscoveryRun | undefined) {
  if (!run) return "Upload logs and start discovery.";
  if (run.state === DiscoveryRunState.FAILED) {
    if (run.error?.message) {
      return `${discoveryStepLabel(run.error.step || run.currentStep)} failed: ${run.error.message}`;
    }
    return run.errorMessage || "Discovery failed without an error message.";
  }
  if (run.state === DiscoveryRunState.SUCCEEDED) return "Discovery completed.";
  return discoveryProgressMessage(run) || run.progressMessage || "Discovery is waiting for progress.";
}

function discoveryProgressMessage(run: DiscoveryRun) {
  const progress = run.progress;
  const labelBatch = progress?.labelBatch;
  if (labelBatch) {
    if (labelBatch.event === "retrying") {
      return `Retrying label batch ${labelBatch.batchNumber}/${labelBatch.batchCount}, attempt ${labelBatch.attempt}/${labelBatch.maxAttempts} (${labelBatch.completedCount} completed).`;
    }
    if (labelBatch.event === "completed") {
      return `Labeled pattern batches ${labelBatch.completedCount}/${labelBatch.batchCount}.`;
    }
    return `Labeling batch ${labelBatch.batchNumber}/${labelBatch.batchCount}, attempt ${labelBatch.attempt}/${labelBatch.maxAttempts} (${labelBatch.batchSize} patterns, ${labelBatch.completedCount} completed).`;
  }

  const step = progress?.step || run.currentStep;
  switch (step) {
    case DiscoveryStep.READING_LOGS:
      return "Reading log files.";
    case DiscoveryStep.MERGING_ENTRIES:
      return `Merging log entries from ${run.logFileCount} files.`;
    case DiscoveryStep.DISCOVERING_PATTERNS:
      return `Discovering log patterns across ${run.logFileCount} files.`;
    case DiscoveryStep.LABELING_PATTERNS:
      return "Preparing pattern labels.";
    case DiscoveryStep.WRITING_RESULTS:
      return "Writing discovery results.";
    default:
      return "";
  }
}

function discoveryStepLabel(step: DiscoveryStep) {
  switch (step) {
    case DiscoveryStep.READING_LOGS:
      return "Reading logs";
    case DiscoveryStep.MERGING_ENTRIES:
      return "Merging entries";
    case DiscoveryStep.DISCOVERING_PATTERNS:
      return "Discovering patterns";
    case DiscoveryStep.LABELING_PATTERNS:
      return "Labeling patterns";
    case DiscoveryStep.WRITING_RESULTS:
      return "Writing results";
    default:
      return "Discovery";
  }
}

function formatBytes(value: bigint) {
  const bytes = Number(value);
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatTimestamp(value: { seconds: bigint | number; nanos: number } | undefined) {
  if (!value) return "";
  const seconds = typeof value.seconds === "bigint" ? Number(value.seconds) : value.seconds;
  return new Date(seconds * 1000 + Math.floor(value.nanos / 1_000_000)).toLocaleString();
}

createRoot(document.getElementById("root")!).render(<App />);
