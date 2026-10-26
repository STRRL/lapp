import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Clock,
  CloudDownload,
  Copy,
  Download,
  Eye,
  File as FileIcon,
  Info,
  KeyRound,
  LoaderCircle,
  OctagonAlert,
  Pencil,
  Plus,
  Search,
  Shield,
  Sparkles,
  Terminal,
  TrendingUp,
  TriangleAlert,
  Upload,
  VolumeX,
  X
} from "lucide-react";
import { InvestigatePanel } from "./investigate";
import "./design.css";
import "./demo.css";

// ---------- API types ----------

type DemoWorkspace = {
  id: string;
  title: string;
  service: string;
  lineCount: number;
  patternCount: number;
  filteredLines: number;
  limitReached?: boolean;
  updatedAt: string;
};

type DemoPattern = {
  id: string;
  name: string;
  named: boolean;
  description?: string;
  template: string;
  count: number;
  level: string;
  buckets: number[];
  suggestion: "noise" | "maybe" | "";
  reason: string;
  keptReason?: string;
  filtered: boolean;
  spikeAt?: number;
  newAt?: number;
  samples: string[];
  sinceLast?: "new" | "up" | "down";
  ratio?: number;
  prevCount?: number;
};

type CompareInfo = {
  previousId: string;
  previousTitle: string;
  previousAt: string;
  new: number;
  changed: number;
  gone: { template: string; name: string; count: number }[];
  options: { id: string; title: string; at: string }[];
  byRate: boolean;
};

type PatternsResponse = {
  workspace: DemoWorkspace;
  fileNames: string[];
  from?: string;
  to?: string;
  bucketStart?: string;
  bucketSeconds: number;
  rulesSaved: boolean;
  rulesApplied: number;
  named: boolean;
  patterns: DemoPattern[];
  unmatched: { count: number; buckets: number[]; samples: string[] };
  compare?: CompareInfo;
};

type FieldStat = { median: number; p95: number; max: number };
type DemoField = {
  index: number;
  kind: "number" | "text";
  unit?: string;
  buckets?: (FieldStat | null)[];
  overall?: FieldStat;
  breakdown?: { value: string; count: number }[];
  other?: number;
};
type PatternDetail = { firstSeen?: string; lastSeen?: string; fields: DemoField[] };
type DemoLine = { n: number; text: string };
type ImportStatus = { state: string; step: string; error?: string; named: boolean; lines: number; batches?: string };

async function getJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  const body = await res.json();
  if (!res.ok) throw new Error(body?.error ?? res.statusText);
  return body as T;
}

// ---------- formatting ----------

// formatPercent is the single place percentages are rounded: 1 decimal, half up.
export function formatPercent(part: number, total: number) {
  if (total === 0) return "0.0%";
  return `${(Math.round((part / total) * 1000) / 10).toFixed(1)}%`;
}

const fmtCount = (n: number) => n.toLocaleString("en-US");
const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
const pad = (n: number) => String(n).padStart(2, "0");
const formatDate = (t: Date) => `${MONTHS[t.getUTCMonth()]} ${t.getUTCDate()}`;
// "Dec 4 06:00" — never "12-04", which reads as a day-month pair.
const formatTime = (t: Date) => `${formatDate(t)} ${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())}`;

function bucketTime(data: PatternsResponse, i: number) {
  return new Date(new Date(data.bucketStart!).getTime() + i * data.bucketSeconds * 1000);
}
function bucketLabel(data: PatternsResponse, i: number) {
  if (!data.bucketStart) return "";
  const t = bucketTime(data, i);
  return data.bucketSeconds >= 86400 ? formatDate(t) : formatTime(t);
}
function atBucket(data: PatternsResponse, i: number) {
  return `${data.bucketSeconds >= 86400 ? "on" : "at"} ${bucketLabel(data, i)}`;
}
function unitName(seconds: number) {
  const names: Record<number, string> = { 60: "minute", 300: "5 min", 900: "15 min", 3600: "hour", 21600: "6 hours", 86400: "day", 604800: "week" };
  return names[seconds] ?? "month";
}
function perUnit(seconds: number) {
  const u = unitName(seconds);
  return /^\d/.test(u) ? ` per ${u}` : `/${u}`;
}
function timeAgo(iso: string) {
  const s = (Date.now() - new Date(iso).getTime()) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.round(s / 60)} min ago`;
  if (s < 86400) return `${Math.round(s / 3600)} h ago`;
  return new Date(iso).toLocaleDateString("en-US", { month: "short", day: "numeric" });
}
function niceMax(v: number) {
  if (v <= 0) return 1;
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}
function fmtNum(x: number) {
  if (Math.abs(x) >= 1e6) return `${+(x / 1e6).toFixed(1)}M`;
  if (Math.abs(x) >= 1e4) return `${+(x / 1e3).toFixed(1)}k`;
  return Number.isInteger(x) ? String(x) : String(+x.toFixed(2));
}
function median(xs: number[]) {
  if (xs.length === 0) return 0;
  const s = [...xs].sort((a, b) => a - b);
  return s[Math.floor((s.length - 1) / 2)];
}
// Same rule as the server: first token of the file name ("Apache_2k.log" → "apache").
function serviceFromFileName(name: string) {
  const stem = name.toLowerCase().replace(/\.[^.]*$/, "");
  return stem.split(/[^a-z0-9]+/).filter(Boolean)[0] ?? "logs";
}

// Series colors are design tokens --s1…--s8; rank 9+ and filtered use --s-other.
const seriesColor = (rank: number) => (rank < 8 ? `var(--s${rank + 1})` : "var(--s-other)");
const OTHER = "var(--s-other)";

function useToast() {
  const [toast, setToast] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const show = useCallback((text: string) => {
    setToast(text);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setToast(null), 4000);
  }, []);
  const node = toast ? (
    <div className="toast toast-fixed" role="status">
      <Check size={16} /> {toast}
    </div>
  ) : null;
  return { show, node };
}

function copyText(text: string, toast: (t: string) => void) {
  navigator.clipboard?.writeText(text).then(() => toast("Copied"), () => undefined);
}

// ---------- shell ----------

export function DemoApp() {
  const read = () => new URLSearchParams(window.location.search);
  const [params, setParams] = useState(read);
  useEffect(() => {
    const onPop = () => setParams(read());
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  const navigate = useCallback((query: Record<string, string>, replace = false) => {
    const qs = new URLSearchParams(query).toString();
    const url = qs ? `?${qs}` : window.location.pathname;
    if (replace) window.history.replaceState(null, "", url);
    else window.history.pushState(null, "", url);
    setParams(read());
  }, []);
  const ws = params.get("ws") ?? "";
  return (
    <div className="dz">
      {ws ? (
        <PatternList key={ws} wsId={ws} patternParam={params.get("pattern")} navigate={navigate} />
      ) : (
        <ImportPage navigate={navigate} />
      )}
    </div>
  );
}

function TopBar({
  crumb,
  navigate,
  full = true,
  extra
}: {
  crumb?: React.ReactNode;
  navigate: (q: Record<string, string>) => void;
  full?: boolean;
  extra?: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const [recent, setRecent] = useState<DemoWorkspace[]>([]);
  useEffect(() => {
    if (open) getJSON<DemoWorkspace[]>("/api/demo/workspaces").then(setRecent).catch(() => setRecent([]));
  }, [open]);
  return (
    <header className="topbar">
      <button className="wordmark linklike" onClick={() => navigate({})}>
        <i />
        lapp
      </button>
      {crumb}
      <div style={{ flex: 1 }} />
      {extra}
      {full && (
        <>
          <div className="menu-anchor">
            <button className="btn ghost sm" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
              <Clock className="ico" /> Recent
            </button>
            {open && (
              <div className="menu" onMouseLeave={() => setOpen(false)}>
                {recent.length === 0 && <div className="menu-empty">No imports yet</div>}
                {recent.map((w) => (
                  <button key={w.id} className="menu-item" onClick={() => { setOpen(false); navigate({ ws: w.id }); }}>
                    <b>{w.service}</b>
                    <span className="mono ell">{w.title}</span>
                    <em className="tnum">{timeAgo(w.updatedAt)}</em>
                  </button>
                ))}
              </div>
            )}
          </div>
          <button className="btn sm" onClick={() => navigate({})}>
            <Plus className="ico" /> Import logs
          </button>
        </>
      )}
    </header>
  );
}

// Click-to-rename service name in the top bar; saved rules follow the new name.
function ServiceName({ wsId, value, onRenamed }: { wsId: string; value: string; onRenamed: (s: string) => void }) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(value);
  const commit = async () => {
    setEditing(false);
    const name = draft.trim();
    if (!name || name === value) return setDraft(value);
    try {
      const res = await getJSON<{ service: string }>(`/api/demo/service?ws=${encodeURIComponent(wsId)}&name=${encodeURIComponent(name)}`, { method: "POST" });
      onRenamed(res.service);
      setDraft(res.service);
    } catch {
      setDraft(value);
    }
  };
  if (editing) {
    return (
      <input
        className="svc-input"
        autoFocus
        value={draft}
        aria-label="Service name"
        onChange={(e) => setDraft(e.target.value)}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") commit();
          if (e.key === "Escape") {
            setDraft(value);
            setEditing(false);
          }
        }}
      />
    );
  }
  const unnamed = value === "stdin";
  return (
    <button
      className={`svc-name ${unnamed ? "is-stdin" : ""}`}
      onClick={() => setEditing(true)}
      title={unnamed ? "Give this service a name to reuse its noise rules next time" : "Rename service"}
    >
      <b>{value}</b>
      <Pencil className="ico pen" />
    </button>
  );
}

// ---------- import page ----------

const STEPS = [
  { key: "fetch", label: "Fetching from GCP", steps: ["IMPORTING"] },
  { key: "read", label: "Reading file", steps: ["READING_LOGS", "MERGING_ENTRIES"] },
  { key: "find", label: "Finding patterns", steps: ["DISCOVERING_PATTERNS"] },
  { key: "name", label: "Naming patterns", steps: ["LABELING_PATTERNS"] },
  { key: "noise", label: "Marking noise", steps: ["WRITING_RESULTS"] }
];

function ImportPage({ navigate }: { navigate: (q: Record<string, string>, replace?: boolean) => void }) {
  const [tab, setTab] = useState<"file" | "gcp">(() => (new URLSearchParams(window.location.search).get("tab") === "gcp" ? "gcp" : "file"));
  const [file, setFile] = useState<File | null>(null);
  const [service, setService] = useState("");
  const [savedRules, setSavedRules] = useState(0);
  const [dragging, setDragging] = useState(false);
  const [recent, setRecent] = useState<DemoWorkspace[] | null>(null);
  const [job, setJob] = useState<{ id: string; name: string } | null>(null);
  const [status, setStatus] = useState<ImportStatus | null>(null);
  const [error, setError] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  const { show, node: toast } = useToast();

  useEffect(() => {
    getJSON<DemoWorkspace[]>("/api/demo/workspaces").then(setRecent).catch(() => setRecent([]));
  }, []);
  useEffect(() => {
    if (!service) {
      setSavedRules(0);
      return;
    }
    const t = window.setTimeout(() => {
      getJSON<{ count: number }>(`/api/demo/service-rules?service=${encodeURIComponent(service)}`)
        .then((r) => setSavedRules(r.count))
        .catch(() => setSavedRules(0));
    }, 150);
    return () => window.clearTimeout(t);
  }, [service]);

  // Poll the import until patterns are ready, then open the list.
  useEffect(() => {
    if (!job) return;
    let stop = false;
    const tick = async () => {
      try {
        const st = await getJSON<ImportStatus>(`/api/demo/status?ws=${encodeURIComponent(job.id)}`);
        if (stop) return;
        setStatus(st);
        if (st.state === "succeeded") return navigate({ ws: job.id });
        if (st.state === "failed") return;
      } catch (e) {
        if (!stop) setStatus({ state: "failed", step: "", error: String(e), named: false, lines: 0 });
        return;
      }
      window.setTimeout(tick, 400);
    };
    tick();
    return () => {
      stop = true;
    };
  }, [job, navigate]);

  const choose = (f: File | undefined) => {
    if (!f) return;
    setFile(f);
    setService(serviceFromFileName(f.name));
    setError("");
  };

  const start = async () => {
    if (!file) return;
    setError("");
    try {
      const res = await getJSON<{ id: string }>(
        `/api/demo/import?name=${encodeURIComponent(file.name)}&service=${encodeURIComponent(service)}`,
        { method: "POST", body: file }
      );
      setStatus(null);
      setJob({ id: res.id, name: file.name });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };

  if (job) {
    return (
      <>
        <TopBar navigate={navigate} full={false} />
        <Processing
          name={job.name}
          gcp={job.name.startsWith("GCP · ")}
          status={status}
          onRetry={() => {
            setJob(null);
            setFile(null);
            setStatus(null);
          }}
        />
      </>
    );
  }

  return (
    <>
      <TopBar navigate={navigate} full={false} />
      <main className="imp">
        <h1>Import logs</h1>
        <div className="tabs big" role="tablist">
          <button className={tab === "file" ? "on" : ""} onClick={() => setTab("file")} role="tab" aria-selected={tab === "file"}>
            <FileIcon className="ico" /> Log file
          </button>
          <button className={tab === "gcp" ? "on" : ""} onClick={() => setTab("gcp")} role="tab" aria-selected={tab === "gcp"}>
            <CloudDownload className="ico" /> Import from GCP
          </button>
        </div>

        {tab === "gcp" ? (
          <GCPForm
            onStarted={(id, name) => {
              setStatus(null);
              setJob({ id, name });
            }}
          />
        ) : (
          <>
            <button
              className={`drop ${dragging ? "is-drag" : ""}`}
              onClick={() => inputRef.current?.click()}
              onDragOver={(e) => {
                e.preventDefault();
                setDragging(true);
              }}
              onDragLeave={() => setDragging(false)}
              onDrop={(e) => {
                e.preventDefault();
                setDragging(false);
                choose(e.dataTransfer.files[0]);
              }}
            >
              <span className="drop-ic">
                <Upload className="ico" />
              </span>
              {file ? (
                <>
                  <b>{file.name}</b>
                  <span className="tnum">{fmtCount(file.size)} bytes · click to choose another file</span>
                </>
              ) : (
                <>
                  <b>Drop a log file here, or click to choose one</b>
                  <span>Plain text or NDJSON (one JSON object per line) · your file is never changed</span>
                </>
              )}
              <input ref={inputRef} type="file" hidden onChange={(e) => choose(e.target.files?.[0])} />
            </button>

            <div className="field">
              <label htmlFor="svc">
                Service <span>Saved noise rules for this service apply automatically</span>
              </label>
              <div className="combo">
                <input id="svc" value={service} placeholder="e.g. api" onChange={(e) => setService(e.target.value)} />
                {savedRules > 0 && (
                  <span className="hintr">
                    <Check className="ico" /> {savedRules} noise {savedRules === 1 ? "rule" : "rules"} saved
                  </span>
                )}
              </div>
            </div>
            {error && <p className="err">Couldn't read this file. {error}</p>}
            <div className="actions">
              <button className="btn primary" disabled={!file} onClick={start}>
                Import
              </button>
            </div>
          </>
        )}

        <div className="privacy">
          <Shield className="ico" />
          <span>
            To name patterns, lapp sends a few sample lines from each pattern to OpenRouter. Investigate sends what your agent reads to its provider. Nothing else leaves
            this machine.
          </span>
        </div>
        <div className="clihint">
          <span className="sub">Or from a terminal</span>
          {["lapp app.log", "kubectl logs pod/api | lapp --name api.log"].map((c) => (
            <button key={c} className="chip mono" onClick={() => copyText(c, show)} title="Copy">
              {c}
            </button>
          ))}
        </div>

        <section className="recent">
          <h3>Recent</h3>
          {recent && recent.length === 0 && (
            <p className="sub">
              Drop in a log file to find its patterns. Or run <code>lapp app.log</code> from your terminal.
            </p>
          )}
          {recent?.map((w) => (
            <button key={w.id} className="rrow" onClick={() => navigate({ ws: w.id })}>
              <b className="ell">{w.service}</b>
              <span className="mono ell">{w.title}</span>
              <span className="tnum">
                {fmtCount(w.lineCount)} lines · {w.patternCount} patterns · {formatPercent(w.filteredLines, w.lineCount)} filtered
              </span>
              <span className="tnum">{timeAgo(w.updatedAt)}</span>
              <ChevronRight className="ico" />
            </button>
          ))}
        </section>
      </main>
      {toast}
    </>
  );
}

function Processing({ name, gcp, status, onRetry }: { name: string; gcp: boolean; status: ImportStatus | null; onRetry: () => void }) {
  const steps = STEPS.filter((s) => gcp || s.key !== "fetch");
  const current = steps.findIndex((s) => s.steps.includes(status?.step || (gcp ? "IMPORTING" : "READING_LOGS")));
  const failed = status?.state === "failed";
  const done = status?.state === "succeeded";
  const idx = done ? steps.length : Math.max(0, current);
  return (
    <main className="imp">
      <h1>{name}</h1>
      <p className="lead">The pattern list opens as soon as this finishes.</p>
      <div className="card steps">
        {steps.map((s, i) => {
          const skipped = s.key === "name" && status !== null && !status.named;
          const state = failed && i === idx ? "fail" : i < idx ? "done" : i === idx ? "run" : "todo";
          let detail = "";
          if ((s.key === "read" || s.key === "fetch") && status?.lines) detail = `${fmtCount(status.lines)} lines`;
          if (skipped && i <= idx) detail = "Skipped — no OpenRouter key";
          if (s.key === "name" && state === "run" && status?.batches) detail = status.batches;
          return (
            <div key={s.key} className={`step ${state}`}>
              <span className="sic">
                {state === "done" && <Check className="ico" />}
                {state === "run" && <LoaderCircle className="ico spin" />}
                {state === "fail" && <X className="ico" />}
              </span>
              <b>{state === "fail" ? (s.key === "fetch" ? "Couldn't import from GCP." : "Couldn't read this file.") : `${s.label}${state === "run" ? "…" : ""}`}</b>
              <span className="tnum">{state === "fail" ? status?.error : detail}</span>
            </div>
          );
        })}
        <div className="pbar">
          <i style={{ width: `${(idx / steps.length) * 100}%` }} />
        </div>
        {failed && (
          <div className="actions left">
            <button className="btn primary" onClick={onRetry}>
              {gcp ? "Try again" : "Choose another file"}
            </button>
          </div>
        )}
      </div>
    </main>
  );
}

const RANGES = [
  { key: "1h", label: "Last 1h" },
  { key: "6h", label: "Last 6h" },
  { key: "24h", label: "Last 24h" },
  { key: "custom", label: "Custom…" }
];

function GCPForm({ onStarted }: { onStarted: (id: string, name: string) => void }) {
  const [project, setProject] = useState("");
  const [service, setService] = useState("");
  const [filter, setFilter] = useState("");
  const [range, setRange] = useState("1h");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState("");
  const [recent, setRecent] = useState<{ projects: string[]; filters: string[] }>({ projects: [], filters: [] });
  useEffect(() => {
    getJSON<{ projects: string[]; filters: string[] }>("/api/demo/gcp-recent").then(setRecent).catch(() => undefined);
  }, []);
  const ready = project.trim() !== "" && (range !== "custom" || (from !== "" && to !== ""));
  const start = async () => {
    setError("");
    const body =
      range === "custom"
        ? { project, filter, service, from: new Date(from).toISOString(), to: new Date(to).toISOString() }
        : { project, filter, service, since: range };
    try {
      const res = await getJSON<{ id: string }>("/api/demo/import-gcp", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
      });
      const label = range === "custom" ? "custom range" : `last ${range}`;
      onStarted(res.id, `GCP · ${project} · ${label}`);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <div className="card form">
      <div className="frow">
        <div className="field">
          <label htmlFor="gcp-project">Project</label>
          <input id="gcp-project" list="gcp-projects" value={project} placeholder="my-project" onChange={(e) => setProject(e.target.value)} />
          <datalist id="gcp-projects">
            {recent.projects.map((p) => (
              <option key={p} value={p} />
            ))}
          </datalist>
        </div>
        <div className="field">
          <label htmlFor="gcp-service">
            Service <span>Saved noise rules for this service apply automatically</span>
          </label>
          <input id="gcp-service" value={service} placeholder={project ? project.toLowerCase() : "e.g. api"} onChange={(e) => setService(e.target.value)} />
        </div>
      </div>
      <div className="field">
        <label htmlFor="gcp-filter">
          Filter <span>optional</span>
        </label>
        <input id="gcp-filter" className="mono" list="gcp-filters" value={filter} placeholder='resource.type="cloud_run_revision" severity>=WARNING' onChange={(e) => setFilter(e.target.value)} />
        <datalist id="gcp-filters">
          {recent.filters.map((f) => (
            <option key={f} value={f} />
          ))}
        </datalist>
      </div>
      <div className="field">
        <label>Time range</label>
        <div className="seg" role="tablist">
          {RANGES.map((r) => (
            <button key={r.key} className={range === r.key ? "on" : ""} onClick={() => setRange(r.key)}>
              {r.label}
            </button>
          ))}
        </div>
        {range === "custom" && (
          <div className="frow" style={{ marginTop: 8 }}>
            <input type="datetime-local" aria-label="From" value={from} onChange={(e) => setFrom(e.target.value)} />
            <input type="datetime-local" aria-label="To" value={to} onChange={(e) => setTo(e.target.value)} />
          </div>
        )}
        <p className="hint">Uses this machine's gcloud credentials. Stops at 100,000 lines.</p>
      </div>
      {error && <p className="err">Couldn't import from GCP. {error}</p>}
      <div className="actions">
        <button className="btn primary" disabled={!ready} onClick={start}>
          Import
        </button>
      </div>
    </div>
  );
}

// ---------- pattern list ----------

function PatternList({
  wsId,
  patternParam,
  navigate
}: {
  wsId: string;
  patternParam: string | null;
  navigate: (q: Record<string, string>, replace?: boolean) => void;
}) {
  const [data, setData] = useState<PatternsResponse | null>(null);
  const [error, setError] = useState("");
  const [filtered, setFiltered] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [sort, setSort] = useState<"lines" | "spiking" | "new">("lines");
  const [showFiltered, setShowFiltered] = useState<boolean | null>(null);
  const [bannerDismissed, setBannerDismissed] = useState(false);
  const [inv, setInv] = useState<{ pattern?: DemoPattern; question: string; reason?: string; spike?: boolean; key: number; jobId?: string } | null>(null);
  const [invHidden, setInvHidden] = useState(false);
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [compareTo, setCompareTo] = useState("");
  const [compareFilter, setCompareFilter] = useState<"new" | "changed" | null>(null);
  const [goneOpen, setGoneOpen] = useState(false);
  const goneRef = useRef<HTMLButtonElement>(null);
  const loaded = useRef(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const filteredGroupRef = useRef<HTMLButtonElement>(null);
  const { show: toast, node: toastNode } = useToast();

  useEffect(() => {
    getJSON<PatternsResponse>(`/api/demo/patterns?ws=${encodeURIComponent(wsId)}${compareTo ? `&compare=${encodeURIComponent(compareTo)}` : ""}`)
      .then((d) => {
        setData(d);
        if (loaded.current) return;
        loaded.current = true;
        setFiltered(new Set(d.patterns.filter((p) => p.filtered).map((p) => p.id)));
        const jobId = new URLSearchParams(window.location.search).get("investigate");
        if (jobId) {
          fetch(`/api/demo/investigate?id=${encodeURIComponent(jobId)}`)
            .then((r) => (r.ok ? r.json() : null))
            .then((job) => {
              if (!job) return;
              const p = d.patterns.find((x) => x.id === job.pattern);
              setInv({ pattern: p, question: job.question, reason: p ? startReason(p, d) : undefined, spike: p?.spikeAt !== undefined, key: Date.now(), jobId });
            });
        }
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [wsId, compareTo]);

  // Colors are assigned once by total-line rank and never repaint.
  const rank = useMemo(() => {
    const m = new Map<string, number>();
    data?.patterns.forEach((p, i) => m.set(p.id, i));
    return m;
  }, [data]);

  const selected: DemoPattern | "unmatched" | null = useMemo(() => {
    if (!data || !patternParam) return null;
    if (patternParam === "unmatched") return "unmatched";
    return data.patterns.find((p) => p.id === patternParam) ?? null;
  }, [data, patternParam]);

  const select = useCallback(
    (id: string | null) => navigate(id ? { ws: wsId, pattern: id } : { ws: wsId }, true),
    [navigate, wsId]
  );
  const openInvestigate = (p?: DemoPattern) => {
    let question = "";
    let reason: string | undefined;
    let spike = false;
    if (p && data) {
      const name = p.named ? p.name : p.template;
      if (p.spikeAt !== undefined) {
        spike = true;
        question = `Why did "${name}" spike ${atBucket(data, p.spikeAt)}? What else changed around then?`;
      } else if (p.newAt !== undefined) {
        question = `Why did "${name}" start appearing ${atBucket(data, p.newAt)}? What else changed around then?`;
      } else {
        question = `What is causing "${name}"? What else changed around then?`;
      }
      reason = startReason(p, data);
    }
    setInv({ pattern: p, question, reason, spike, key: Date.now() });
    setInvHidden(false);
    select(null);
  };

  const toggle = useCallback((id: string) => {
    setFiltered((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const hasTrend = !!data && data.bucketSeconds > 0;
  const matches = (p: DemoPattern) => {
    if (compareFilter === "new" && p.sinceLast !== "new") return false;
    if (compareFilter === "changed" && p.sinceLast !== "up" && p.sinceLast !== "down") return false;
    if (errorsOnly && !["error", "warn", "fatal"].includes(p.level)) return false;
    if (!query) return true;
    const q = query.toLowerCase();
    return p.name.toLowerCase().includes(q) || p.template.toLowerCase().includes(q);
  };
  const sorter = (a: DemoPattern, b: DemoPattern) => {
    if (sort === "spiking") {
      const sa = a.spikeAt !== undefined ? a.buckets[a.spikeAt] : -1;
      const sb = b.spikeAt !== undefined ? b.buckets[b.spikeAt] : -1;
      return sb - sa || b.count - a.count;
    }
    if (sort === "new") return (b.newAt ?? -1) - (a.newAt ?? -1) || b.count - a.count;
    return b.count - a.count;
  };
  const visible = data ? data.patterns.filter((p) => !filtered.has(p.id)) : [];
  const hidden = data ? data.patterns.filter((p) => filtered.has(p.id)) : [];
  const visibleRows = visible.filter(matches).sort(sorter);
  const hiddenRows = hidden.filter(matches).sort(sorter);
  const filteredOpen = showFiltered ?? hidden.length <= 5;
  const order = [...visibleRows, ...(filteredOpen ? hiddenRows : [])];

  // Keyboard: "/" focuses search, Esc closes detail, ↑/↓ move between patterns.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLInputElement || e.target instanceof HTMLSelectElement;
      if (e.key === "/" && !typing) {
        e.preventDefault();
        searchRef.current?.focus();
      } else if (e.key === "Escape" && selected && !typing) {
        select(null);
      } else if (e.key === "Escape" && inv && !invHidden && !typing) {
        setInv(null);
      } else if ((e.key === "ArrowDown" || e.key === "ArrowUp") && selected && selected !== "unmatched" && !typing) {
        e.preventDefault();
        const i = order.findIndex((p) => p.id === selected.id);
        const next = order[i + (e.key === "ArrowDown" ? 1 : -1)];
        if (next) select(next.id);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (error) {
    return (
      <>
        <TopBar navigate={navigate} />
        <p className="err pad">{error}</p>
      </>
    );
  }
  if (!data) return <ListSkeleton navigate={navigate} />;

  const total = data.workspace.lineCount;
  const filteredLines = hidden.reduce((s, p) => s + p.count, 0);
  const left = total - filteredLines;
  const autoNoise = data.patterns.filter((p) => p.suggestion === "noise" && filtered.has(p.id));
  const autoNoiseLines = autoNoise.reduce((s, p) => s + p.count, 0);
  const fileName = data.fileNames[0] ?? data.workspace.title;
  const service = data.workspace.service;
  const colorOf = (p: DemoPattern) => (filtered.has(p.id) ? OTHER : seriesColor(rank.get(p.id) ?? 99));

  const save = async () => {
    await getJSON(`/api/demo/rules?ws=${encodeURIComponent(wsId)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ filtered: [...filtered] })
    });
    setBannerDismissed(true);
    toast(`Noise rules saved for ${service}. They'll apply next time.`);
  };
  const unfilterAll = () => {
    setFiltered(new Set());
    setBannerDismissed(true);
  };

  const exportHref =
    `/api/demo/export?ws=${encodeURIComponent(wsId)}` +
    (filtered.size === 0 ? "&filtered=" : [...filtered].map((id) => `&filtered=${encodeURIComponent(id)}`).join(""));

  let banner: React.ReactNode = null;
  if (!bannerDismissed && data.rulesSaved && data.rulesApplied > 0) {
    banner = (
      <div className="callout">
        <VolumeX className="ico" />
        <div>
          Applied {data.rulesApplied} saved noise {data.rulesApplied === 1 ? "rule" : "rules"} for <b>{service}</b>.
        </div>
        <button className="btn sm ghost" onClick={unfilterAll}>
          Unfilter all
        </button>
      </div>
    );
  } else if (!bannerDismissed && !data.rulesSaved && autoNoise.length > 0) {
    banner = (
      <div className="callout">
        <VolumeX className="ico" />
        <div>
          <b>
            lapp filtered {autoNoise.length} {autoNoise.length === 1 ? "pattern that looks" : "patterns that look"} like noise ({fmtCount(autoNoiseLines)} lines,{" "}
            {formatPercent(autoNoiseLines, total)}).
          </b>{" "}
          Based on share and repetition, not log level. Save to apply the same rules to <b>{service}</b> logs next time.
        </div>
        <button className="btn sm primary" onClick={save}>
          <Check className="ico" /> Save noise rules
        </button>
        <button className="btn sm ghost" onClick={unfilterAll}>
          Unfilter all
        </button>
      </div>
    );
  }

  return (
    <>
      <TopBar
        navigate={navigate}
        extra={
          <button className="btn sm" onClick={() => openInvestigate()}>
            <Sparkles className="ico" /> Ask about these logs
          </button>
        }
        crumb={
          <>
            <span className="sep">/</span>
            <div className="crumb">
              <ServiceName
                wsId={wsId}
                value={service}
                onRenamed={(s) => {
                  setData({ ...data, workspace: { ...data.workspace, service: s } });
                  toast(`Noise rules will be saved for ${s}.`);
                }}
              />
              <span className="tnum">
                {data.workspace.title.startsWith("GCP · ") ? data.workspace.title : fileName} · {fmtCount(total)} lines
                {data.from && data.to ? ` · ${formatTime(new Date(data.from))} – ${formatTime(new Date(data.to))}` : ""}
              </span>
              {data.workspace.limitReached && (
                <span className="badge b-warn" title="Stopped at 100,000 lines. Narrow the time range or filter to see everything.">
                  <TriangleAlert className="ico" />
                  limit reached
                </span>
              )}
            </div>
          </>
        }
      />
      <main className={`lp ${selected || (inv && !invHidden) ? "with-drawer" : ""}`}>
        <section className="lp-main">
          <div className="summary">
            <div className="meter">
              <div className="meter-num tnum">
                <b>{formatPercent(filteredLines, total)} filtered</b>
                <span>
                  <b>{fmtCount(left)}</b> lines left · <b>{visible.length}</b> patterns · <b>{fmtCount(data.unmatched.count)}</b> unmatched lines
                </span>
              </div>
              <div className="meter-bar" role="img" aria-label={`${formatPercent(filteredLines, total)} filtered`}>
                <i style={{ width: formatPercent(left, total) }} />
              </div>
            </div>
            <div style={{ flex: 1 }} />
            <button className="cli mono hide-narrow" title="Copy command" onClick={() => copyText(`lapp filter ${fileName} > clean.log`, toast)}>
              <Terminal className="ico" />
              <span>lapp filter {fileName} &gt; clean.log</span>
              <Copy className="ico" />
            </button>
            <a className="btn primary" href={exportHref}>
              <Download className="ico" /> Export {fmtCount(left)} lines
            </a>
          </div>
          {data.compare && (
            <CompareRow
              c={data.compare}
              filter={compareFilter}
              onChoose={(id) => {
                setCompareTo(id);
                setCompareFilter(null);
              }}
              onFilter={(f) => setCompareFilter((cur) => (cur === f ? null : f))}
              onGone={() => {
                setGoneOpen(true);
                window.setTimeout(() => goneRef.current?.scrollIntoView({ behavior: "smooth" }), 0);
              }}
            />
          )}

          {!data.named && (
            <div className="banner">
              <KeyRound className="ico" />
              <div>Patterns are shown as raw text. Set OPENROUTER_API_KEY to get readable names.</div>
            </div>
          )}

          <StartHere data={data} items={startHere(visible)} colorOf={colorOf} onOpen={(p) => select(p.id)} onInvestigate={(p) => openInvestigate(p)} />

          <section className="card overview">
            <header>
              <h2>Remaining lines over time</h2>
              <span className="sub tnum">{hasTrend ? `Per ${unitName(data.bucketSeconds)} · filtered patterns not shown` : ""}</span>
              <div style={{ flex: 1 }} />
              {hasTrend && (
                <div className="legend">
                  {visible.slice(0, 8).map((p) => (
                    <span key={p.id}>
                      <i style={{ background: colorOf(p) }} />
                      {short(p.name, p.named ? 32 : 22)}
                    </span>
                  ))}
                  {visible.length > 8 && (
                    <span>
                      <i style={{ background: OTHER }} />
                      Other
                    </span>
                  )}
                </div>
              )}
            </header>
            {hasTrend ? (
              <Overview data={data} patterns={visible} colorOf={colorOf} highlight={hoverId} />
            ) : (
              <div className="notime-chart">
                <span className="sub">
                  <Clock className="ico" /> Trend unavailable — no timestamps found in these lines
                </span>
              </div>
            )}
          </section>

          {banner}

          <div className="toolbar">
            <label className="search">
              <Search className="ico" />
              <input ref={searchRef} placeholder="Search names or templates" aria-label="Search patterns" value={query} onChange={(e) => setQuery(e.target.value)} />
            </label>
            <div className="seg" role="tablist">
              <button className={!errorsOnly ? "on" : ""} onClick={() => setErrorsOnly(false)}>
                All
              </button>
              <button className={errorsOnly ? "on" : ""} onClick={() => setErrorsOnly(true)}>
                Errors and warnings only
              </button>
            </div>
            <div style={{ flex: 1 }} />
            {!banner && (
              <button className="btn sm" onClick={save}>
                Save noise rules
              </button>
            )}
            <label className="sel">
              Sort
              <select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)}>
                <option value="lines">Most lines</option>
                <option value="spiking" disabled={!hasTrend}>
                  Spiking
                </option>
                <option value="new" disabled={!hasTrend}>
                  New
                </option>
              </select>
            </label>
          </div>

          <section className="table card" role="table">
            <div className="thead" role="row">
              <span>Filter</span>
              <span>Pattern</span>
              <span className="col-level">Level</span>
              <span>{hasTrend ? `Trend (${unitName(data.bucketSeconds) === "hour" ? "hourly" : `per ${unitName(data.bucketSeconds)}`})` : "Trend"}</span>
              <span>Lines · Share</span>
              <span />
            </div>
            <div className="group-h">
              To review · {visible.length} patterns · {fmtCount(left)} lines
            </div>
            {visible.length === 0 && (
              <div className="emptyall">
                <VolumeX className="ico" />
                Everything here is filtered as noise.
                <button
                  className="btn sm"
                  onClick={() => {
                    setShowFiltered(true);
                    window.setTimeout(() => filteredGroupRef.current?.scrollIntoView({ behavior: "smooth" }), 0);
                  }}
                >
                  Show filtered patterns
                </button>
              </div>
            )}
            {query && visibleRows.length + hiddenRows.length === 0 && (
              <div className="noresult">
                No patterns match "{query}".
                <button className="btn sm ghost" onClick={() => setQuery("")}>
                  Clear search
                </button>
              </div>
            )}
            {visibleRows.map((p) => (
              <Row
                key={p.id}
                p={p}
                data={data}
                total={total}
                color={colorOf(p)}
                filtered={false}
                selected={selected !== "unmatched" && selected?.id === p.id}
                hovered={hoverId === p.id}
                onToggle={toggle}
                onOpen={() => select(selected !== "unmatched" && selected?.id === p.id ? null : p.id)}
              />
            ))}
            <div
              className={`prow unmatched ${selected === "unmatched" ? "selected" : ""}`}
              role="row"
              tabIndex={0}
              onClick={() => select(selected === "unmatched" ? null : "unmatched")}
              onKeyDown={(e) => e.key === "Enter" && select("unmatched")}
            >
              <span />
              <div className="cell-main">
                <div className="pname">
                  <i className="dot" style={{ background: "var(--muted)" }} />
                  <b>Unmatched lines</b>
                </div>
                <div className="ptpl">Lines that fit no pattern are listed here and are never filtered.</div>
              </div>
              <span className="col-level" />
              <div className="cell-trend">
                {hasTrend && data.unmatched.count > 0 && <Spark buckets={data.unmatched.buckets} color="var(--muted)" label="Unmatched lines" unit={data.bucketSeconds} />}
              </div>
              <div className="cell-count tnum">
                <b>{fmtCount(data.unmatched.count)}</b>
                <span>{formatPercent(data.unmatched.count, total)}</span>
              </div>
              <span className="cell-chev">{data.unmatched.count > 0 && <ChevronRight className="ico" />}</span>
            </div>
            {hidden.length > 0 && (
              <>
                <button ref={filteredGroupRef} className="group-h filt grouptoggle" onClick={() => setShowFiltered(!filteredOpen)} aria-expanded={filteredOpen}>
                  {filteredOpen ? <ChevronDown className="ico" /> : <ChevronRight className="ico" />} Filtered · {hidden.length} patterns · {fmtCount(filteredLines)} lines
                </button>
                {filteredOpen &&
                  hiddenRows.map((p) => (
                    <Row
                      key={p.id}
                      p={p}
                      data={data}
                      total={total}
                      color={OTHER}
                      filtered
                      hovered={hoverId === p.id}
                      selected={selected !== "unmatched" && selected?.id === p.id}
                      onToggle={toggle}
                      onOpen={() => select(selected !== "unmatched" && selected?.id === p.id ? null : p.id)}
                    />
                  ))}
              </>
            )}
            {data.compare && data.compare.gone.length > 0 && (
              <>
                <button ref={goneRef} className="group-h filt grouptoggle" onClick={() => setGoneOpen((v) => !v)} aria-expanded={goneOpen}>
                  {goneOpen ? <ChevronDown className="ico" /> : <ChevronRight className="ico" />} Gone since last time · {data.compare.gone.length} patterns
                </button>
                {goneOpen &&
                  data.compare.gone.map((g) => (
                    <div key={g.template} className="prow gone" role="row">
                      <span />
                      <div className="cell-main">
                        <div className="pname nowrap">
                          <i className="dot" style={{ background: OTHER }} />
                          <b className="unnamed ell">
                            <Template text={g.name} />
                          </b>
                        </div>
                      </div>
                      <span className="col-level" />
                      <span />
                      <div className="cell-count tnum">
                        <b>0</b>
                        <span>{fmtCount(g.count)} last time</span>
                      </div>
                      <span />
                    </div>
                  ))}
              </>
            )}
          </section>
        </section>
        {inv && !selected && (
          <>
            <div className="scrim" onClick={() => setInv(null)} />
            <InvestigatePanel
              key={inv.key}
              wsId={wsId}
              data={data}
              start={inv}
              colorOf={(p) => colorOf(p as DemoPattern)}
              filtered={filtered}
              onClose={() => setInv(null)}
              onCite={(p) => {
                setInvHidden(true);
                setHoverId(null);
                select(p.id);
              }}
              onHoverPattern={setHoverId}
              onJob={(id) => window.history.replaceState(null, "", `?ws=${encodeURIComponent(wsId)}&investigate=${id}`)}
              toast={toast}
            />
          </>
        )}
        {selected && (
          <>
            <div className="scrim" onClick={() => select(null)} />
            <Drawer
              onBack={
                inv
                  ? () => {
                      setInvHidden(false);
                      select(null);
                    }
                  : undefined
              }
              onInvestigate={selected !== "unmatched" ? () => openInvestigate(selected) : undefined}
              key={selected === "unmatched" ? "unmatched" : selected.id}
              wsId={wsId}
              data={data}
              target={selected}
              color={selected === "unmatched" ? "var(--muted)" : colorOf(selected)}
              filtered={selected !== "unmatched" && filtered.has(selected.id)}
              onToggle={toggle}
              onShowOnly={(p) => setQuery(p.named ? p.name : p.template)}
              onClose={() => select(null)}
              toast={toast}
            />
          </>
        )}
      </main>
      {toastNode}
    </>
  );
}

function short(s: string, n: number) {
  return s.length > n ? `${s.slice(0, n)}…` : s;
}

const isError = (p: DemoPattern) => p.level === "error" || p.level === "fatal";
const isNew = (p: DemoPattern) => p.newAt !== undefined || p.sinceLast === "new";

// "Start here": rule-based shortlist of the patterns most likely to be the
// problem — spiking errors, new errors, other spikes, other new (max 5).
export function startHere(patterns: DemoPattern[]) {
  const spike = (p: DemoPattern) => (p.spikeAt !== undefined ? p.buckets[p.spikeAt] : 0);
  const vsLast = (p: DemoPattern) => isError(p) && p.spikeAt === undefined && (p.sinceLast === "new" || p.sinceLast === "up");
  const groups = [
    patterns.filter((p) => isError(p) && p.spikeAt !== undefined).sort((a, b) => spike(b) - spike(a)),
    patterns.filter(vsLast).sort((a, b) => (b.ratio ?? 99) - (a.ratio ?? 99) || b.count - a.count),
    patterns.filter((p) => isError(p) && p.spikeAt === undefined && !vsLast(p) && isNew(p)).sort((a, b) => b.count - a.count),
    patterns.filter((p) => !isError(p) && p.spikeAt !== undefined).sort((a, b) => spike(b) - spike(a)),
    patterns.filter((p) => !isError(p) && p.spikeAt === undefined && isNew(p)).sort((a, b) => b.count - a.count)
  ];
  return groups.flat().slice(0, 5);
}

function startReason(p: DemoPattern, data: PatternsResponse) {
  if (isError(p) && p.spikeAt === undefined && p.sinceLast === "new") return "New error since last time";
  if (isError(p) && p.spikeAt === undefined && p.sinceLast === "up") return `Error up ×${p.ratio} vs last time`;
  const parts: string[] = [];
  if (isError(p)) parts.push("Error");
  if (p.spikeAt !== undefined) parts.push(`spiked to ${p.buckets[p.spikeAt]}${perUnit(data.bucketSeconds)} ${atBucket(data, p.spikeAt)}`);
  else if (p.newAt !== undefined) parts.push(`first seen ${bucketLabel(data, p.newAt)}`);
  else if (p.sinceLast === "new") parts.push("new since last time");
  const text = parts.join(" · ");
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function StartHere({
  data,
  items,
  colorOf,
  onOpen,
  onInvestigate
}: {
  data: PatternsResponse;
  items: DemoPattern[];
  colorOf: (p: DemoPattern) => string;
  onOpen: (p: DemoPattern) => void;
  onInvestigate?: (p: DemoPattern) => void;
}) {
  return (
    <section className="card starthere">
      <header>
        <h2>Start here</h2>
        <span className="sub">Patterns most likely to be the problem</span>
      </header>
      {items.length === 0 ? (
        <p className="sub sh-empty">Nothing stands out: no errors, spikes or new patterns.</p>
      ) : (
        items.map((p) => (
          <div key={p.id} className="sh-row" role="button" tabIndex={0} onClick={() => onOpen(p)} onKeyDown={(e) => e.key === "Enter" && onOpen(p)}>
            <i className="dot" style={{ background: colorOf(p) }} />
            <div className="sh-main">
              {p.named ? (
                <b className="ell">{p.name}</b>
              ) : (
                <b className="unnamed ell">
                  <Template text={p.name} />
                </b>
              )}
              <span className={`sh-why ${isError(p) ? "is-err" : ""}`}>
                {isError(p) ? <OctagonAlert className="ico" /> : p.spikeAt !== undefined ? <TrendingUp className="ico" /> : <Sparkles className="ico" />}
                {startReason(p, data)}
              </span>
            </div>
            <span className="sh-count tnum">{fmtCount(p.count)} lines</span>
            {onInvestigate && (
              <button
                className="btn sm inv-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  onInvestigate(p);
                }}
              >
                <Sparkles className="ico" /> Investigate
              </button>
            )}
            <ChevronRight className="ico cell-chev" />
          </div>
        ))
      )}
    </section>
  );
}

function CompareRow({
  c,
  filter,
  onChoose,
  onFilter,
  onGone
}: {
  c: CompareInfo;
  filter: "new" | "changed" | null;
  onChoose: (id: string) => void;
  onFilter: (f: "new" | "changed") => void;
  onGone: () => void;
}) {
  const label = (o: { title: string; at: string }) => `${formatTime(new Date(o.at))} · ${o.title}`;
  const none = c.new + c.gone.length + c.changed === 0;
  return (
    <div className="cmp-row">
      <label className="sel">
        Compare with
        <select value={c.previousId} onChange={(e) => onChoose(e.target.value)}>
          {c.options.map((o) => (
            <option key={o.id} value={o.id}>
              {label(o)}
            </option>
          ))}
          <option value="none">Don't compare</option>
        </select>
      </label>
      {none ? (
        <span className="sub">No changes since last time: same patterns, similar volumes.</span>
      ) : (
        <span className="cmp-facts">
          Since last time:{" "}
          <button className={`linkbtn ${filter === "new" ? "on" : ""}`} onClick={() => onFilter("new")} disabled={!c.new}>
            {c.new} new
          </button>{" "}
          ·{" "}
          <button className="linkbtn" onClick={onGone} disabled={!c.gone.length}>
            {c.gone.length} gone
          </button>{" "}
          ·{" "}
          <button className={`linkbtn ${filter === "changed" ? "on" : ""}`} onClick={() => onFilter("changed")} disabled={!c.changed}>
            {c.changed} changed a lot
          </button>
        </span>
      )}
    </div>
  );
}

function ListSkeleton({ navigate }: { navigate: (q: Record<string, string>) => void }) {
  return (
    <>
      <TopBar navigate={navigate} />
      <main className="lp">
        <section className="lp-main skel" aria-busy="true">
          <span className="sk" style={{ width: 320, height: 22 }} />
          <div className="card overview" style={{ height: 180 }} />
          <div className="card table">
            {[0, 1, 2].map((i) => (
              <div key={i} className="prow">
                <span />
                <div className="cell-main">
                  <span className="sk" style={{ width: "40%" }} />
                  <span className="sk" style={{ width: "70%", marginTop: 8 }} />
                </div>
              </div>
            ))}
          </div>
        </section>
      </main>
    </>
  );
}

function Template({ text, active }: { text: string; active?: number }) {
  const parts = text.split("<*>");
  return (
    <>
      {parts.map((part, i) => (
        <React.Fragment key={i}>
          {part}
          {i < parts.length - 1 && <span className={`ph ${active === i ? "ph-on" : ""}`}>&lt;*&gt;</span>}
        </React.Fragment>
      ))}
    </>
  );
}

function LevelBadge({ level }: { level: string }) {
  if (!level) return <span className="notime">—</span>;
  if (level === "error" || level === "fatal")
    return (
      <span className="badge b-err">
        <OctagonAlert className="ico" />
        {level}
      </span>
    );
  if (level === "warn")
    return (
      <span className="badge b-warn">
        <TriangleAlert className="ico" />
        warn
      </span>
    );
  return <span className="badge b-info">{level}</span>;
}

function Row({
  p,
  data,
  total,
  color,
  filtered,
  selected,
  hovered,
  onToggle,
  onOpen
}: {
  p: DemoPattern;
  data: PatternsResponse;
  total: number;
  color: string;
  filtered: boolean;
  selected: boolean;
  hovered?: boolean;
  onToggle: (id: string) => void;
  onOpen: () => void;
}) {
  const share = formatPercent(p.count, total);
  const hasTrend = data.bucketSeconds > 0;
  return (
    <div
      className={`prow ${filtered ? "filtered" : ""} ${selected ? "selected" : ""} ${hovered ? "hovered" : ""}`}
      role="row"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter") onOpen();
        if (e.key === " ") {
          e.preventDefault();
          onToggle(p.id);
        }
      }}
    >
      <label className="cell-check" title={filtered ? "Show this pattern" : "Filter this pattern"} onClick={(e) => e.stopPropagation()}>
        <input type="checkbox" checked={filtered} aria-label={`Filter ${p.name}`} onChange={() => onToggle(p.id)} />
        <span className="cb">
          <Check className="ico" />
        </span>
      </label>
      <div className="cell-main">
        {!p.named && (
          <div className="pname nowrap">
            <i className="dot" style={{ background: color }} />
            <b className="unnamed ell" title="Not named yet — add an OpenRouter key to get readable names">
              <Template text={p.name} />
            </b>
          </div>
        )}
        <div className={`pname ${p.named ? "" : "badges-only"}`}>
          {p.named && (
            <>
              <i className="dot" style={{ background: color }} />
              <b>{p.name}</b>
            </>
          )}
          <span className="level-inline">
            <LevelBadge level={p.level} />
          </span>
          {!filtered && p.spikeAt !== undefined && (
            <span className="badge b-spike">
              <TrendingUp className="ico" />
              Spiked to {p.buckets[p.spikeAt]}
              {perUnit(data.bucketSeconds)} {atBucket(data, p.spikeAt)}
            </span>
          )}
          {!filtered && p.newAt !== undefined && (
            <span className="badge b-new">
              <Sparkles className="ico" />
              First seen {bucketLabel(data, p.newAt)}
            </span>
          )}
          {!filtered && p.sinceLast === "new" && (
            <span className="badge b-new">
              <Sparkles className="ico" />
              New since last time
            </span>
          )}
          {p.suggestion === "noise" && (
            <span className="badge b-noise" title={filtered ? "Filtered automatically. Uncheck to show it again." : undefined}>
              <VolumeX className="ico" />
              Likely noise
            </span>
          )}
          {p.suggestion === "maybe" && !filtered && (
            <span className="badge b-maybe" title="Not filtered. Decide for yourself.">
              <Info className="ico" />
              Maybe noise
            </span>
          )}
        </div>
        {p.named && (
          <div className="ptpl mono">
            <Template text={p.template} />
          </div>
        )}
        {p.suggestion === "noise" && <div className="why">Why it looks like noise: {p.reason}</div>}
        {p.suggestion === "maybe" && !filtered && <div className="why">Why it's not filtered: {p.keptReason}</div>}
      </div>
      <div className="cell-level col-level">
        <LevelBadge level={p.level} />
      </div>
      <div className="cell-trend">
        {hasTrend ? (
          <Spark buckets={p.buckets} color={color} mark={filtered ? undefined : p.spikeAt} label={p.name} unit={data.bucketSeconds} />
        ) : (
          <span className="notime" title="Trend unavailable — no timestamps found in these lines">
            —
          </span>
        )}
      </div>
      <div className="cell-count tnum">
        <b>{fmtCount(p.count)}</b>
        <span>{share}</span>
        <div className="sharebar">
          <i style={{ width: share, background: color }} />
        </div>
        {!filtered && (p.sinceLast === "up" || p.sinceLast === "down") && (
          <em className={`vs-last ${p.sinceLast}`}>
            {p.sinceLast === "up" ? "Up" : "Down"} ×{p.ratio} vs last time
          </em>
        )}
      </div>
      <div className="cell-chev">
        <ChevronRight className="ico" />
      </div>
    </div>
  );
}

function Spark({ buckets, color, mark, label, unit }: { buckets: number[]; color: string; mark?: number; label: string; unit: number }) {
  const W = 168;
  const H = 34;
  const maxV = Math.max(1, ...buckets);
  const bw = W / Math.max(buckets.length, 1);
  return (
    <svg width={W} height={H} className="spark" role="img" aria-label={`${label} lines per ${unitName(unit)}, peak ${maxV}`}>
      <line x1={0} x2={W} y1={H - 0.5} y2={H - 0.5} stroke="var(--line)" />
      {buckets.map((v, i) =>
        v > 0 ? <rect key={i} x={i * bw + 0.5} width={Math.max(1, bw - 1.5)} y={H - 1 - (v / maxV) * (H - 6)} height={(v / maxV) * (H - 6)} rx={1} fill={color} /> : null
      )}
      {mark !== undefined && <circle cx={mark * bw + bw / 2} cy={3} r={2.5} fill="var(--serious)" />}
    </svg>
  );
}

function useWidth<T extends HTMLElement>(fallback: number) {
  const ref = useRef<T>(null);
  const [w, setW] = useState(fallback);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setW(Math.max(240, el.clientWidth)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

function Overview({
  data,
  patterns,
  colorOf,
  highlight
}: {
  data: PatternsResponse;
  patterns: DemoPattern[];
  colorOf: (p: DemoPattern) => string;
  highlight?: string | null;
}) {
  const [ref, W] = useWidth<HTMLDivElement>(1000);
  const [hover, setHover] = useState<number | null>(null);
  const n = data.unmatched.buckets.length;
  // Stacked in list order, largest at the bottom; unmatched lines on top.
  const series = [
    ...patterns.map((p) => ({ id: p.id, name: p.name, color: colorOf(p), buckets: p.buckets })),
    { id: "~unmatched", name: "Unmatched lines", color: "var(--muted)", buckets: data.unmatched.buckets }
  ];
  const totals = Array.from({ length: n }, (_, i) => series.reduce((s, x) => s + x.buckets[i], 0));
  const maxV = niceMax(Math.max(1, ...totals));
  const H = 132;
  const top = 6;
  const padL = 36;
  const bw = (W - padL) / Math.max(n, 1);
  const every = Math.max(1, Math.ceil(n / Math.max(1, Math.floor((W - padL) / 120))));
  const y = (v: number) => top + H - (v / maxV) * H;
  return (
    <div className="chart-wrap" ref={ref} onMouseLeave={() => setHover(null)}>
      <svg width={W} height={H + top + 22} role="img" aria-label="Remaining lines over time, stacked by pattern">
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line x1={padL} x2={W} y1={y(maxV * f)} y2={y(maxV * f)} stroke="var(--line)" />
            <text x={padL - 8} y={y(maxV * f) + 4} textAnchor="end" className="axis">
              {fmtNum(maxV * f)}
            </text>
          </g>
        ))}
        {totals.map((_, i) => {
          let acc = 0;
          const x = padL + i * bw;
          return (
            <g key={i} onMouseEnter={() => setHover(i)}>
              <rect x={x} y={top} width={bw} height={H} fill={hover === i ? "var(--ring)" : "transparent"} />
              {series.map((s) => {
                const v = s.buckets[i];
                if (!v) return null;
                const y0 = y(acc);
                acc += v;
                const h = Math.max(0.5, y0 - y(acc) - (acc > v ? 1 : 0));
                return (
                  <rect key={s.id} x={x + 1} width={Math.max(1, bw - 2)} y={y(acc)} height={h} rx={1.5} fill={s.color} opacity={highlight && highlight !== s.id ? 0.3 : 1} />
                );
              })}
              {i % every === 0 && (
                <text x={x} y={H + top + 16} className="axis">
                  {bucketLabel(data, i)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      {hover !== null && totals[hover] > 0 && (
        <div className="tooltip tnum" style={{ left: Math.min(padL + (hover + 1) * bw + 8, W - 240), top: 8 }}>
          <div className="tt-h">
            {data.bucketSeconds >= 86400 ? bucketLabel(data, hover) : `${bucketLabel(data, hover)} – ${bucketLabel(data, hover + 1).split(" ").pop()}`}
          </div>
          {series
            .filter((s) => s.buckets[hover] > 0)
            .sort((a, b) => b.buckets[hover] - a.buckets[hover])
            .slice(0, 10)
            .map((s) => (
              <div key={s.id} className="tt-row">
                <i style={{ background: s.color }} />
                <span>{short(s.name, 38)}</span>
                <b>{s.buckets[hover]}</b>
              </div>
            ))}
          <div className="tt-f">Total {totals[hover]} lines</div>
        </div>
      )}
    </div>
  );
}

// ---------- detail panel ----------

function Drawer({
  wsId,
  data,
  target,
  color,
  filtered,
  onToggle,
  onShowOnly,
  onClose,
  onBack,
  onInvestigate,
  toast
}: {
  onBack?: () => void;
  onInvestigate?: () => void;
  wsId: string;
  data: PatternsResponse;
  target: DemoPattern | "unmatched";
  color: string;
  filtered: boolean;
  onToggle: (id: string) => void;
  onShowOnly: (p: DemoPattern) => void;
  onClose: () => void;
  toast: (t: string) => void;
}) {
  const [lines, setLines] = useState<DemoLine[] | null>(null);
  const [shown, setShown] = useState(200);
  const [detail, setDetail] = useState<PatternDetail | null>(null);
  const isUnmatched = target === "unmatched";
  const q = isUnmatched ? "" : `&pattern=${encodeURIComponent(target.id)}`;
  useEffect(() => {
    getJSON<DemoLine[]>(`/api/demo/lines?ws=${encodeURIComponent(wsId)}${q}`).then(setLines).catch(() => setLines([]));
    if (!isUnmatched) getJSON<PatternDetail>(`/api/demo/pattern?ws=${encodeURIComponent(wsId)}${q}`).then(setDetail).catch(() => setDetail({ fields: [] }));
  }, [wsId, q, isUnmatched]);
  const total = data.workspace.lineCount;
  const count = isUnmatched ? data.unmatched.count : target.count;
  const buckets = isUnmatched ? data.unmatched.buckets : target.buckets;

  return (
    <aside className="drawer" aria-label="Pattern detail">
      {onBack && (
        <button className="btn ghost sm backlink" onClick={onBack}>
          ← Back to investigation
        </button>
      )}
      <header>
        <div>
          <div className="pname">
            <i className="dot" style={{ background: color }} />
            {isUnmatched ? (
              <b className="dtitle">Unmatched lines</b>
            ) : target.named ? (
              <b className="dtitle">{target.name}</b>
            ) : (
              <b className="dtitle unnamed">
                <Template text={target.name} />
              </b>
            )}
            {!isUnmatched && <LevelBadge level={target.level} />}
          </div>
          {!isUnmatched && target.named && target.description && <p className="desc">{target.description}</p>}
        </div>
        <button className="btn ghost sm" aria-label="Close" onClick={onClose}>
          <X className="ico" />
        </button>
      </header>
      {!isUnmatched && (
        <div className="ptpl mono boxed">
          <Template text={target.template} />
        </div>
      )}
      <div className="dstats tnum">
        <div>
          <b>{fmtCount(count)}</b>
          <span>lines</span>
        </div>
        <div>
          <b>{formatPercent(count, total)}</b>
          <span>of all lines</span>
        </div>
        <div>
          <b>{detail?.firstSeen ? formatTime(new Date(detail.firstSeen)) : "—"}</b>
          <span>First seen</span>
        </div>
        <div>
          <b>{detail?.lastSeen ? formatTime(new Date(detail.lastSeen)) : "—"}</b>
          <span>Last seen</span>
        </div>
      </div>
      {!isUnmatched && (
        <>
          <div className="dactions">
            <label className="chk">
              <input type="checkbox" checked={filtered} onChange={() => onToggle(target.id)} />
              <span className="cb">
                <Check className="ico" />
              </span>
              Filter this pattern
            </label>
            <button className="btn sm" onClick={() => onShowOnly(target)}>
              <Eye className="ico" /> Show only this
            </button>
            <button className="btn sm ghost" onClick={() => copyText(target.template, toast)}>
              <Copy className="ico" /> Copy template
            </button>
            {onInvestigate && (
              <button className="btn sm inv-btn" onClick={onInvestigate}>
                <Sparkles className="ico" /> Investigate
              </button>
            )}
          </div>
          {target.suggestion === "noise" && <div className="why">Why it looks like noise: {target.reason}</div>}
          {target.suggestion === "maybe" && <div className="why">Why it's not filtered: {target.keptReason}</div>}
        </>
      )}

      <section className="dsec">
        {data.bucketSeconds > 0 ? (
          <OverTime data={data} buckets={buckets} color={color} spikeAt={isUnmatched ? undefined : target.spikeAt} />
        ) : (
          <>
            <h3>Over time</h3>
            <p className="sub">Trend unavailable — no timestamps found in these lines</p>
          </>
        )}
      </section>

      {!isUnmatched && (
        <section className="dsec">
          <h3>Fields</h3>
          {detail === null && <p className="sub">Loading…</p>}
          {detail && detail.fields.length === 0 && <p className="sub">No variable fields in this pattern.</p>}
          {detail?.fields.map((f) => (
            <FieldBlock key={f.index} field={f} data={data} color={color} template={target.template} />
          ))}
        </section>
      )}

      <section className="dsec">
        <h3>Lines ({fmtCount(count)})</h3>
        <div className="raw mono">
          {lines === null && <div className="rawl sub">Loading…</div>}
          {lines?.slice(0, shown).map((l) => (
            <div key={l.n} className="rawl">
              <span className="ln">{l.n}</span>
              <span className="rawtext">{l.text}</span>
              <button className="cp" title="Copy line" onClick={() => copyText(l.text, toast)}>
                <Copy className="ico" />
              </button>
            </div>
          ))}
        </div>
        {lines && (
          <div className="rawfoot">
            {lines.length > shown && (
              <button className="btn sm" onClick={() => setShown((n) => n + 200)}>
                Load more
              </button>
            )}
            <span className="sub tnum">
              Showing {fmtCount(Math.min(shown, lines.length))} of {fmtCount(count)}
            </span>
          </div>
        )}
      </section>
    </aside>
  );
}

function OverTime({ data, buckets, color, spikeAt }: { data: PatternsResponse; buckets: number[]; color: string; spikeAt?: number }) {
  const [ref, W] = useWidth<HTMLDivElement>(530);
  const [hover, setHover] = useState<number | null>(null);
  const H = 150;
  const top = 18;
  const padL = 32;
  const maxV = niceMax(Math.max(1, ...buckets));
  const med = median(buckets.filter((v) => v > 0));
  const bw = (W - padL) / Math.max(buckets.length, 1);
  const y = (v: number) => top + H - (v / maxV) * H;
  const every = Math.max(1, Math.ceil(buckets.length / Math.max(1, Math.floor((W - padL) / 110))));
  return (
    <div ref={ref}>
      <h3>
        Over time{" "}
        <span className="sub">
          · dashed line = median, {fmtNum(med)} {med === 1 ? "line" : "lines"}
          {perUnit(data.bucketSeconds)}
        </span>
      </h3>
      <div className="chart-wrap" onMouseLeave={() => setHover(null)}>
        <svg width={W} height={H + top + 22} role="img" aria-label={`Lines per ${unitName(data.bucketSeconds)}`}>
          {[0, 0.5, 1].map((f) => (
            <g key={f}>
              <line x1={padL} x2={W} y1={y(maxV * f)} y2={y(maxV * f)} stroke="var(--line)" />
              <text x={padL - 6} y={y(maxV * f) + 4} textAnchor="end" className="axis">
                {fmtNum(maxV * f)}
              </text>
            </g>
          ))}
          {buckets.map((v, i) => (
            <g key={i} onMouseEnter={() => setHover(i)}>
              <rect x={padL + i * bw} y={top} width={bw} height={H} fill={hover === i ? "var(--ring)" : "transparent"} />
              {v > 0 && <rect x={padL + i * bw + 1} width={Math.max(1, bw - 2)} y={y(v)} height={y(0) - y(v)} rx={1.5} fill={color} />}
              {i % every === 0 && (
                <text x={padL + i * bw} y={H + top + 16} className="axis">
                  {bucketLabel(data, i)}
                </text>
              )}
            </g>
          ))}
          {med > 0 && <line x1={padL} x2={W} y1={y(med)} y2={y(med)} stroke="var(--ink-3)" strokeDasharray="4 3" pointerEvents="none" />}
          {spikeAt !== undefined && (
            <>
              <circle cx={padL + spikeAt * bw + bw / 2} cy={y(buckets[spikeAt]) - 6} r={3} fill="var(--serious)" />
              <text x={Math.min(padL + spikeAt * bw + bw / 2 + 8, W - 230)} y={Math.max(12, y(buckets[spikeAt]) - 2)} className="axis lbl">
                Spiked to {buckets[spikeAt]}
                {perUnit(data.bucketSeconds)} {atBucket(data, spikeAt)}
              </text>
            </>
          )}
        </svg>
        {hover !== null && (
          <div className="tooltip tnum" style={{ left: Math.min(padL + (hover + 1) * bw + 6, W - 200), top: 8, minWidth: 0 }}>
            {bucketLabel(data, hover)}: {buckets[hover]} lines
          </div>
        )}
      </div>
    </div>
  );
}

// Template excerpt around the field's <*>: ~24 characters each side, "…" at cut ends.
function FieldContext({ template, index }: { template: string; index: number }) {
  const parts = template.split("<*>");
  const before = parts.slice(0, index + 1).join("<*>");
  const after = parts.slice(index + 1).join("<*>");
  const b = before.length > 24 ? `…${before.slice(-24)}` : before;
  const a = after.length > 24 ? `${after.slice(0, 24)}…` : after;
  const offset = (b.match(/<\*>/g) ?? []).length;
  return (
    <div className="ptpl mono fieldctx">
      <Template text={`${b}<*>${a}`} active={offset} />
    </div>
  );
}

function FieldBlock({ field, data, color, template }: { field: DemoField; data: PatternsResponse; color: string; template: string }) {
  const ctx = <FieldContext template={template} index={field.index - 1} />;
  if (field.kind === "text") {
    const rows = [...(field.breakdown ?? []), ...(field.other ? [{ value: "Other", count: field.other }] : [])];
    const sum = rows.reduce((s, r) => s + r.count, 0);
    const maxC = Math.max(1, ...rows.map((r) => r.count));
    return (
      <div className="fieldblk">
        <div className="fieldh">
          Field {field.index} <span className="sub">— Breakdown by value</span>
        </div>
        {ctx}
        <div className="enum">
          {rows.map((r) => (
            <div key={r.value} className="ebar">
              <span className="mono ell" title={r.value}>
                {r.value}
              </span>
              <div>
                <i style={{ width: `${(r.count / maxC) * 100}%`, background: r.value === "Other" && field.other ? OTHER : color }} />
              </div>
              <b className="tnum">{fmtCount(r.count)}</b>
              <em className="tnum">{formatPercent(r.count, sum)}</em>
            </div>
          ))}
        </div>
      </div>
    );
  }
  return <NumberField field={field} data={data} color={color} ctx={ctx} />;
}

function NumberField({ field, data, color, ctx }: { field: DemoField; data: PatternsResponse; color: string; ctx: React.ReactNode }) {
  const [ref, W] = useWidth<HTMLDivElement>(530);
  const [hover, setHover] = useState<number | null>(null);
  const buckets = field.buckets ?? [];
  const u = field.unit ?? "";
  const H = 130;
  const top = 8;
  const padL = 48;
  const padR = 48;
  const maxV = niceMax(Math.max(1e-9, ...buckets.map((b) => b?.max ?? 0)));
  const bw = (W - padL - padR) / Math.max(buckets.length, 1);
  const y = (v: number) => top + H - (v / maxV) * H;
  const xs = (i: number) => padL + i * bw + bw / 2;
  const line = (k: keyof FieldStat) =>
    buckets
      .map((b, i) => (b ? `${xs(i)},${y(b[k])}` : null))
      .filter(Boolean)
      .join(" ");
  let lastI = -1;
  buckets.forEach((b, i) => {
    if (b) lastI = i;
  });
  const last = lastI >= 0 ? buckets[lastI] : null;
  const every = Math.max(1, Math.ceil(buckets.length / Math.max(1, Math.floor((W - padL) / 110))));
  const o = field.overall!;
  const hb = hover !== null ? buckets[hover] : null;
  return (
    <div className="fieldblk" ref={ref}>
      <div className="fieldh">
        Field {field.index} (numbers){" "}
        <span className="sub">
          — per {unitName(data.bucketSeconds)}
          {u ? ` · ${u}` : ""} · Median {fmtNum(o.median)}
          {u} · p95 {fmtNum(o.p95)}
          {u} · Max {fmtNum(o.max)}
          {u}
        </span>
      </div>
      {ctx}
      {buckets.length > 0 && (
        <>
          <div className="legend">
            <span>
              <i className="ln-solid" style={{ borderColor: color }} />
              Median
            </span>
            <span>
              <i className="ln-mid" style={{ borderColor: color }} />
              p95
            </span>
            <span>
              <i className="ln-dash" style={{ borderColor: color }} />
              Max
            </span>
          </div>
          <div className="chart-wrap" onMouseLeave={() => setHover(null)}>
            <svg width={W} height={H + top + 22} role="img" aria-label={`Field ${field.index}: median, p95 and max per ${unitName(data.bucketSeconds)}`}>
              {[0, 0.5, 1].map((f) => (
                <g key={f}>
                  <line x1={padL} x2={W - padR} y1={y(maxV * f)} y2={y(maxV * f)} stroke="var(--line)" />
                  <text x={padL - 6} y={y(maxV * f) + 4} textAnchor="end" className="axis">
                    {fmtNum(maxV * f)}
                    {u}
                  </text>
                </g>
              ))}
              <polyline points={line("max")} fill="none" stroke={color} strokeWidth={2} strokeDasharray="5 4" opacity={0.4} />
              <polyline points={line("p95")} fill="none" stroke={color} strokeWidth={2} opacity={0.55} />
              <polyline points={line("median")} fill="none" stroke={color} strokeWidth={2} />
              {last && (
                <>
                  <text x={xs(lastI) + 6} y={y(last.median) + 4} className="axis lbl">
                    Median
                  </text>
                  <text x={xs(lastI) + 6} y={y(last.p95) - 2} className="axis">
                    p95
                  </text>
                  <text x={xs(lastI) + 6} y={y(last.max) - 2} className="axis">
                    Max
                  </text>
                </>
              )}
              {buckets.map((_, i) => (
                <g key={i} onMouseEnter={() => setHover(i)}>
                  <rect x={padL + i * bw} y={top} width={bw} height={H} fill="transparent" />
                  {i % every === 0 && (
                    <text x={padL + i * bw} y={H + top + 16} className="axis">
                      {bucketLabel(data, i)}
                    </text>
                  )}
                </g>
              ))}
              {hover !== null && <line x1={xs(hover)} x2={xs(hover)} y1={top} y2={top + H} stroke="var(--line-strong)" pointerEvents="none" />}
            </svg>
            {hb && hover !== null && (
              <div className="tooltip tnum" style={{ left: Math.min(xs(hover) + 8, W - 200), top: 8, minWidth: 0 }}>
                <div className="tt-h">{bucketLabel(data, hover)}</div>
                <div>
                  Median {fmtNum(hb.median)}
                  {u}
                </div>
                <div>
                  p95 {fmtNum(hb.p95)}
                  {u}
                </div>
                <div>
                  Max {fmtNum(hb.max)}
                  {u}
                </div>
              </div>
            )}
          </div>
          <p className="hint">p95 means 95% of values are at or below this line.</p>
        </>
      )}
    </div>
  );
}
