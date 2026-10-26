import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  ChevronDown,
  ChevronRight,
  Clock,
  CloudDownload,
  Copy,
  Download,
  Eye,
  EyeOff,
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
import { PreviewDialog } from "./preview";
import { Placeholder, ValueHoverProvider } from "./valuepop";
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
  // Imported with an older lapp: listed, but has to be imported again.
  olderVersion?: boolean;
  truncatedLines?: number;
  updatedAt: string;
};

const OLDER_IMPORT_NOTE = "Made with an older version of lapp. Import it again to view.";
const OLDER_IMPORT_HINT = "lapp now reads levels differently, so older imports can't be opened.";

type NoiseDecision = "hide" | "unhide" | "not-noise";

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
  steadyError?: boolean;
  spikeAt?: number;
  newAt?: number;
  samples: string[];
  sinceLast?: "new" | "up" | "down";
  ratio?: number;
  prevCount?: number;
  // Series color index, saved per service by the server (assignColors in colors.go).
  color: number;
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
  keySet: boolean;
  namingApproved: boolean;
  patterns: DemoPattern[];
  unmatched: { count: number; buckets: number[]; samples: string[] };
  compare?: CompareInfo;
  // Every "Start here" candidate in order, hidden patterns included.
  startHere: { id: string; reason: string }[];
  splitMode?: "MERGED" | "PER_LINE";
  physicalLineCount?: number;
  cappedEntryCount?: number;
  time: TimeSource;
};

// Where line times came from; explains a missing volume chart.
type TimeSource = {
  format: "text" | "json" | "csv";
  timed: number;
  lines: number;
  fields?: string[];
  columns?: string[];
  column?: string;
  columnUnreadable?: boolean;
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
type ImportTab = "file" | "gcp" | "victorialogs";

type ImportStatus = { state: string; step: string; error?: string; errorCode?: string; named: boolean; lines: number; batches?: string };

async function getJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  const body = await res.json();
  if (!res.ok) throw Object.assign(new Error(body?.error ?? res.statusText), { body });
  return body as T;
}

// ---------- formatting ----------

// formatPercent is the single place percentages are rounded: 1 decimal, half up.
export function formatPercent(part: number, total: number) {
  if (total === 0) return "0.0%";
  return `${(Math.round((part / total) * 1000) / 10).toFixed(1)}%`;
}

const fmtCount = (n: number) => n.toLocaleString("en-US");
// "1 line" / "2,140 lines": counts shown to people agree with their noun.
const countOf = (n: number, noun: string) => `${fmtCount(n)} ${noun}${n === 1 ? "" : "s"}`;
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
  return ` per ${unitName(seconds)}`;
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

// Series colors are the design tokens --s1…--s8 (values in design.css). Only the
// patterns the volume chart draws as their own series (volumeSplit) show their
// saved color; every other pattern, hidden ones included, uses --s-other.
const SERIES_COLORS = 8;
const OTHER = "var(--s-other)";
// The one color for a pattern everywhere: list dot, trend, volume chart, legend, Start here, detail, Investigate.
const patternColor = (p: DemoPattern, charted: boolean) =>
  charted && p.color >= 0 && p.color < SERIES_COLORS ? `var(--s${p.color + 1})` : OTHER;

// The one way a pattern name is shown: on one line, cut to n characters, and
// apart from every other pattern in the workspace. Where two cut names would
// read the same, each starts from its first difference with a leading "…"
// ("…SELECT * FROM public.get_sample…"), backed up to the start of that word.
// fieldOffset is the number of <*> fields cut off the front, for Template.
type PatternLabel = { text: string; fieldOffset: number };
type LabelOf = (p: { id: string; name: string }, n: number) => PatternLabel;

// Character budgets per place; the list and Start here also cut on width.
const NAME_ROW = 72;
const NAME_NOTICE = 40;
const NAME_LEGEND = 32;
const NAME_TIP = 38;

// Cuts s to n characters with a trailing "…", never inside a <*> field.
function cutName(s: string, n: number) {
  if (s.length <= n) return s;
  const field = s.lastIndexOf("<*>", n - 1);
  const end = field >= 0 && field + 3 > n ? field : n;
  return `${s.slice(0, end)}…`;
}

function patternLabels(names: string[], n: number): PatternLabel[] {
  const flat = names.map((s) => s.replace(/\s+/g, " ").trim());
  const cut = flat.map((s) => cutName(s, n));
  const byCut = new Map<string, number[]>();
  cut.forEach((c, i) => byCut.set(c, [...(byCut.get(c) ?? []), i]));
  return flat.map((name, i) => {
    let from = 0;
    for (const j of byCut.get(cut[i])!) {
      const other = flat[j];
      if (other === name) continue;
      let k = 0;
      while (k < name.length && name[k] === other[k]) k++;
      from = Math.max(from, k);
    }
    if (from === 0) return { text: cut[i], fieldOffset: 0 };
    const word = name.lastIndexOf(" ", from - 1) + 1;
    if (from - word <= 12) from = word;
    const field = name.lastIndexOf("<*>", from - 1);
    if (field >= 0 && field + 3 > from) from = field;
    return { text: `…${cutName(name.slice(from), n - 1)}`, fieldOffset: name.slice(0, from).split("<*>").length - 1 };
  });
}

// labelOf for one workspace's patterns: labels are computed once per length.
function patternNamer(patterns: { id: string; name: string }[]): LabelOf {
  const byLength = new Map<number, Map<string, PatternLabel>>();
  return (p, n) => {
    let labels = byLength.get(n);
    if (!labels) {
      const list = patternLabels(patterns.map((x) => x.name), n);
      labels = new Map(patterns.map((x, i) => [x.id, list[i]]));
      byLength.set(n, labels);
    }
    return labels.get(p.id) ?? patternLabels([p.name], n)[0];
  };
}

function useToast() {
  const [toast, setToast] = useState<string | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const show = useCallback((text: string, ms = 4000) => {
    setToast(text);
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setToast(null), ms);
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
                  <button
                    key={w.id}
                    className={w.olderVersion ? "menu-item older" : "menu-item"}
                    title={w.olderVersion ? OLDER_IMPORT_HINT : undefined}
                    onClick={() => { setOpen(false); navigate(w.olderVersion ? { service: w.service } : { ws: w.id }); }}
                  >
                    <b>{w.service}</b>
                    <span className="ell">
                      <span className="mono">{w.title}</span>
                      {w.olderVersion && <small>{OLDER_IMPORT_NOTE}</small>}
                    </span>
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
  const [tab, setTab] = useState<ImportTab>(() => {
    const t = new URLSearchParams(window.location.search).get("tab");
    return t === "gcp" || t === "victorialogs" ? t : "file";
  });
  const [file, setFile] = useState<File | null>(null);
  // Import again from an older import fills in its service.
  const [service, setService] = useState(() => new URLSearchParams(window.location.search).get("service") ?? "");
  const [savedRules, setSavedRules] = useState(0);
  const [dragging, setDragging] = useState(false);
  const [recent, setRecent] = useState<DemoWorkspace[] | null>(null);
  const [job, setJob] = useState<{ id: string; name: string } | null>(null);
  const [serviceJobs, setServiceJobs] = useState<{ name: string; workspaces: StartedService[] } | null>(null);
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

  if (serviceJobs) {
    return (
      <>
        <TopBar navigate={navigate} full={false} />
        <ServicesProcessing name={serviceJobs.name} workspaces={serviceJobs.workspaces} navigate={navigate} onRetry={() => setServiceJobs(null)} />
      </>
    );
  }

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
          <button className={tab === "victorialogs" ? "on" : ""} onClick={() => setTab("victorialogs")} role="tab" aria-selected={tab === "victorialogs"}>
            <CloudDownload className="ico" /> Import from VictoriaLogs
          </button>
        </div>

        {tab === "victorialogs" ? (
          <VictoriaLogsForm onStarted={(name, workspaces) => setServiceJobs({ name, workspaces })} />
        ) : tab === "gcp" ? (
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
                  <span>Plain text, NDJSON, or a Grafana export (CSV or JSON) · your file is never changed</span>
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
          {recent?.map((w) =>
            w.olderVersion ? (
              <button
                key={w.id}
                className="rrow older"
                title={OLDER_IMPORT_HINT}
                onClick={() => {
                  setService(w.service);
                  navigate({ service: w.service });
                  window.scrollTo({ top: 0, behavior: "smooth" });
                }}
              >
                <b className="ell">{w.service}</b>
                <span className="ell older-title">
                  <span className="mono">{w.title}</span>
                  <small>{OLDER_IMPORT_NOTE}</small>
                </span>
                <span className="tnum">{timeAgo(w.updatedAt)}</span>
                <ChevronRight className="ico" />
              </button>
            ) : (
            <button key={w.id} className="rrow" onClick={() => navigate({ ws: w.id })}>
              <b className="ell">{w.service}</b>
              <span className="mono ell">{w.title}</span>
              <span className="tnum">
                {fmtCount(w.lineCount)} lines · {w.patternCount} patterns · {formatPercent(w.filteredLines, w.lineCount)} filtered
              </span>
              <span className="tnum">{timeAgo(w.updatedAt)}</span>
              <ChevronRight className="ico" />
            </button>
            )
          )}
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
  const [detailsOpen, setDetailsOpen] = useState(false);
  return (
    <main className="imp">
      <h1>{name}</h1>
      <p className="lead">{failed ? (gcp ? "Nothing was imported." : "Nothing was imported. Your file wasn't changed.") : "The pattern list opens as soon as this finishes."}</p>
      <div className="card steps">
        {steps.map((s, i) => {
          const skipped = s.key === "name" && status !== null && !status.named;
          const state = failed && i === idx ? "fail" : i < idx ? "done" : i === idx ? "run" : "todo";
          let detail = "";
          if ((s.key === "read" || s.key === "fetch") && status?.lines) detail = `${fmtCount(status.lines)} lines`;
          if (skipped && i <= idx) detail = "Skipped — no OpenRouter key";
          if (s.key === "name" && state === "run" && status?.batches) detail = status.batches;
          return (
            <React.Fragment key={s.key}>
              <div className={`step ${state}`}>
                <span className="sic">
                  {state === "done" && <Check className="ico" />}
                  {state === "run" && <LoaderCircle className="ico spin" />}
                  {state === "fail" && <X className="ico" />}
                </span>
                <b>{state === "fail" ? (s.key === "fetch" ? "Couldn't import from GCP." : "Couldn't read this file.") : `${s.label}${state === "run" ? "…" : ""}`}</b>
                <span className="tnum">{state === "fail" ? "" : detail}</span>
              </div>
              {state === "fail" && (
                <div className="step-fail">
                  {status?.errorCode === "FORMAT_UNRECOGNIZED" && (
                    <p>lapp reads plain text, NDJSON, or Grafana exports (CSV or JSON).</p>
                  )}
                  {status?.error && (
                    <>
                      <button className="linklike sub" onClick={() => setDetailsOpen((v) => !v)} aria-expanded={detailsOpen}>
                        {detailsOpen ? <ChevronDown className="ico" /> : <ChevronRight className="ico" />} Details
                      </button>
                      {detailsOpen && <p className="mono sub">{status.error}</p>}
                    </>
                  )}
                </div>
              )}
            </React.Fragment>
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

type StartedService = { id: string; service: string };

// VictoriaLogs imports split by service: one workspace per service, so
// progress is a list rather than the single-file step view.
function ServicesProcessing({
  name,
  workspaces,
  navigate,
  onRetry
}: {
  name: string;
  workspaces: StartedService[];
  navigate: (q: Record<string, string>, replace?: boolean) => void;
  onRetry: () => void;
}) {
  const [statuses, setStatuses] = useState<Record<string, ImportStatus>>({});
  useEffect(() => {
    let stop = false;
    const tick = async () => {
      const next: Record<string, ImportStatus> = {};
      await Promise.all(
        workspaces.map(async (w) => {
          try {
            next[w.id] = await getJSON<ImportStatus>(`/api/demo/status?ws=${encodeURIComponent(w.id)}`);
          } catch (e) {
            next[w.id] = { state: "failed", step: "", error: String(e), named: false, lines: 0 };
          }
        })
      );
      if (stop) return;
      setStatuses(next);
      if (workspaces.every((w) => next[w.id]?.state === "succeeded" || next[w.id]?.state === "failed")) return;
      window.setTimeout(tick, 600);
    };
    tick();
    return () => {
      stop = true;
    };
  }, [workspaces]);
  const anyFailed = workspaces.some((w) => statuses[w.id]?.state === "failed");
  return (
    <main className="imp">
      <h1>{name}</h1>
      <p className="lead">
        {workspaces.length} {workspaces.length === 1 ? "service" : "services"}, one workspace each. Saved noise rules for each service apply automatically.
      </p>
      <section className="recent">
        {workspaces.map((w) => {
          const st = statuses[w.id];
          const done = st?.state === "succeeded";
          const failed = st?.state === "failed";
          const detail = failed ? st?.error || "Import failed." : done ? "Patterns ready" : st?.step === "IMPORTING" ? "Fetching from VictoriaLogs…" : st?.state === "running" ? "Finding patterns…" : "Waiting…";
          return (
            <button key={w.id} className="rrow" disabled={!done} onClick={() => navigate({ ws: w.id })}>
              <b className="ell">{w.service}</b>
              <span className="ell">{detail}</span>
              <span className="tnum">{st?.lines ? `${fmtCount(st.lines)} lines` : ""}</span>
              <span>
                {done && <Check className="ico" />}
                {failed && <X className="ico" />}
                {!done && !failed && <LoaderCircle className="ico spin" />}
              </span>
              <ChevronRight className="ico" />
            </button>
          );
        })}
      </section>
      {anyFailed && (
        <div className="actions left">
          <button className="btn primary" onClick={onRetry}>
            Try again
          </button>
        </div>
      )}
    </main>
  );
}

function VictoriaLogsForm({ onStarted }: { onStarted: (name: string, workspaces: StartedService[]) => void }) {
  const [url, setUrl] = useState("");
  const [query, setQuery] = useState("");
  const [range, setRange] = useState("1h");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState("");
  const ready = url.trim() !== "" && (range !== "custom" || (from !== "" && to !== ""));
  const start = async () => {
    setError("");
    const body =
      range === "custom"
        ? { url, query, from: new Date(from).toISOString(), to: new Date(to).toISOString() }
        : { url, query, since: range };
    try {
      const res = await getJSON<{ workspaces: StartedService[] }>("/api/demo/import-victorialogs", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body)
      });
      const label = range === "custom" ? "custom range" : `last ${range}`;
      let host = url;
      try {
        host = new URL(url).host;
      } catch {
        // keep the URL as typed
      }
      onStarted(`VictoriaLogs · ${host} · ${label}`, res.workspaces);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  };
  return (
    <div className="card form">
      <div className="field">
        <label htmlFor="vl-url">URL</label>
        <input id="vl-url" className="mono" value={url} placeholder="http://localhost:9428" onChange={(e) => setUrl(e.target.value)} />
      </div>
      <div className="field">
        <label htmlFor="vl-query">
          LogsQL filter <span>optional, defaults to all logs</span>
        </label>
        <input id="vl-query" className="mono" value={query} placeholder="error OR warn" onChange={(e) => setQuery(e.target.value)} />
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
        <p className="hint">
          Each log stream becomes a service, named by its service.name, service, app, or job label. Credentials come from VICTORIALOGS_* variables where lapp runs. Stops at
          100,000 lines per service.
        </p>
      </div>
      {error && <p className="err">Couldn't import from VictoriaLogs. {error}</p>}
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
  // Set when this import was made with an older lapp and has to be imported again.
  const [olderService, setOlderService] = useState<string | null>(null);
  const [filtered, setFiltered] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [sort, setSort] = useState<"lines" | "spiking" | "new" | "rarest">("lines");
  const [showFiltered, setShowFiltered] = useState<boolean | null>(null);
  const [leaving, setLeaving] = useState<Set<string>>(new Set());
  const [flash, setFlash] = useState<{ id: string; key: number } | null>(null);
  const [inv, setInv] = useState<{ pattern?: DemoPattern; question: string; reason?: string; spike?: boolean; key: number; jobId?: string } | null>(null);
  const [invHidden, setInvHidden] = useState(false);
  const [hoverId, setHoverId] = useState<string | null>(null);
  const [compareTo, setCompareTo] = useState("");
  const [previewOpen, setPreviewOpen] = useState(false);
  const [reload, setReload] = useState(0);
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
        // First time a service could be named: show what would be sent.
        const seenKey = `lapp-naming-asked-${d.workspace.service}`;
        if (d.keySet && !d.named && !d.namingApproved && !sessionStorage.getItem(seenKey)) {
          sessionStorage.setItem(seenKey, "1");
          setPreviewOpen(true);
        }
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
      .catch((e) => {
        if (e?.body?.olderVersion) setOlderService(e.body.service ?? "");
        else setError(e instanceof Error ? e.message : String(e));
      });
  }, [wsId, compareTo, reload]);

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

  // Every Hide / Not noise / Unhide is saved for the service right away.
  const decide = (ids: string[], decision: NoiseDecision) => {
    if (!data || ids.length === 0) return;
    const service = data.workspace.service;
    const edit = (set: Set<string>, add: boolean) => {
      const next = new Set(set);
      ids.forEach((id) => (add ? next.add(id) : next.delete(id)));
      return next;
    };
    if (decision === "hide") {
      setLeaving((prev) => edit(prev, true));
      window.setTimeout(() => {
        setFiltered((prev) => edit(prev, true));
        setLeaving((prev) => edit(prev, false));
      }, 200);
    } else {
      setFiltered((prev) => edit(prev, false));
    }
    if (decision === "not-noise") {
      setData({ ...data, patterns: data.patterns.map((p) => (ids.includes(p.id) ? { ...p, suggestion: "", reason: "", keptReason: "" } : p)) });
    }
    const done =
      decision === "hide"
        ? `Hidden. lapp will hide ${ids.length === 1 ? "it" : "them"} in ${service} next time.`
        : decision === "not-noise"
          ? `Got it. lapp won't flag it in ${service} again.`
          : `Unhidden for ${service}.`;
    getJSON(`/api/demo/rules?ws=${encodeURIComponent(wsId)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ patterns: ids, decision })
    })
      .then(() => {
        toast(done, 2000);
        // Refetch so a pattern moving into the top 8 gets its collision-free color.
        setReload((n) => n + 1);
      })
      .catch((e) => toast(`Couldn't save: ${e instanceof Error ? e.message : String(e)}`));
  };
  const toggle = (id: string) => decide([id], filtered.has(id) ? "unhide" : "hide");
  // Scroll to a pattern row and highlight it briefly.
  const jumpTo = (id: string, retry = true) => {
    const row = [...document.querySelectorAll<HTMLElement>(".prow[data-pattern]")].find((el) => el.dataset.pattern === id);
    if (!row) {
      if (!retry) return;
      setQuery("");
      setErrorsOnly(false);
      setCompareFilter(null);
      window.setTimeout(() => jumpTo(id, false), 0);
      return;
    }
    row.scrollIntoView({ behavior: "smooth", block: "center" });
    setFlash({ id, key: Date.now() });
  };

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
    if (sort === "rarest") return a.count - b.count;
    return b.count - a.count;
  };
  const visible = data ? data.patterns.filter((p) => !filtered.has(p.id)) : [];
  const volume = volumeSplit(visible);
  const charted = new Set(volume.top.map((p) => p.id));
  const labelOf = useMemo(() => patternNamer(data?.patterns ?? []), [data?.patterns]);
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

  if (olderService !== null) {
    return (
      <>
        <TopBar navigate={navigate} />
        <div className="pad older-import">
          <p>This import was made with an older version of lapp.</p>
          <button className="btn" onClick={() => navigate(olderService ? { service: olderService } : {})}>
            Import again
          </button>
        </div>
      </>
    );
  }
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
  const hiddenText = filteredLines === 0 ? "Nothing hidden" : `${formatPercent(filteredLines, total)} hidden`;
  const likelyNoise = visible.filter((p) => p.suggestion === "noise" && !leaving.has(p.id));
  const likelyNoiseLines = likelyNoise.reduce((s, p) => s + p.count, 0);
  const fileName = data.fileNames[0] ?? data.workspace.title;
  const service = data.workspace.service;
  const colorOf = (p: DemoPattern) => patternColor(p, charted.has(p.id));

  const exportHref =
    `/api/demo/export?ws=${encodeURIComponent(wsId)}` +
    (filtered.size === 0 ? "&filtered=" : [...filtered].map((id) => `&filtered=${encodeURIComponent(id)}`).join(""));

  let banner: React.ReactNode = null;
  if (likelyNoise.length > 0) {
    const n = likelyNoise.length;
    banner = (
      <div className="callout noise-notice">
        <EyeOff className="ico" />
        <div className="ell">
          {n} {n === 1 ? "pattern looks" : "patterns look"} like noise:{" "}
          {likelyNoise.slice(0, 3).map((p, i) => (
            <React.Fragment key={p.id}>
              {i > 0 && ", "}
              <button className="noise-name" title={p.name} onClick={() => jumpTo(p.id)}>
                {labelOf(p, NAME_NOTICE).text}
              </button>
            </React.Fragment>
          ))}
          {n > 3 && ` +${n - 3} more`} · {countOf(likelyNoiseLines, "line")} ({formatPercent(likelyNoiseLines, total)}).
        </div>
        <button className="btn sm" onClick={() => decide(likelyNoise.map((p) => p.id), "hide")}>
          {n === 1 ? "Hide it?" : "Hide all"}
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
                  toast(`Renamed to ${s}. Your Hide and Not noise choices are saved under this name.`);
                }}
              />
              <span className="tnum">
                {/^(GCP|VictoriaLogs) · /.test(data.workspace.title) ? data.workspace.title : fileName} · {fmtCount(total)} lines
                {data.from && data.to ? ` · ${formatTime(new Date(data.from))} – ${formatTime(new Date(data.to))}` : ""}
              </span>
              {data.splitMode === "PER_LINE" && <span className="sub">Split by line: no timestamps at line starts.</span>}
              {data.splitMode === "MERGED" && !!data.physicalLineCount && (
                <span className="sub tnum">
                  Joined multi-line logs: {fmtCount(data.physicalLineCount)} raw lines → {fmtCount(total)} lines
                  {data.cappedEntryCount ? ` · ${fmtCount(data.cappedEntryCount)} cut at 500 lines or 1 MB` : ""}
                </span>
              )}
              {data.workspace.limitReached && (
                <span className="badge b-warn" title="Stopped at 100,000 lines. Narrow the time range or filter to see everything.">
                  <TriangleAlert className="ico" />
                  limit reached
                </span>
              )}
              {!!data.workspace.truncatedLines && (
                <span className="badge b-warn">
                  <TriangleAlert className="ico" />
                  {data.workspace.truncatedLines === 1
                    ? "1 line was too long and was cut at 16 MB."
                    : `${fmtCount(data.workspace.truncatedLines)} lines were too long and were cut at 16 MB.`}
                </span>
              )}
            </div>
          </>
        }
      />
      <ValueHoverProvider
        value={{
          wsId,
          colorOf: (id) => {
            const p = data.patterns.find((x) => x.id === id);
            return p ? colorOf(p) : OTHER;
          },
          onShowChart: (id) => select(id)
        }}
      >
      <main className={`lp ${selected || (inv && !invHidden) ? "with-drawer" : ""}`}>
        <section className="lp-main">
          {banner}

          <div className="summary">
            <div className="meter">
              <div className="meter-num tnum">
                <b>{hiddenText}</b>
                <span>
                  <b>{fmtCount(left)}</b> lines left · <b>{visible.length}</b> patterns · <b>{fmtCount(data.unmatched.count)}</b> unmatched lines
                </span>
              </div>
              <div className="meter-bar" role="img" aria-label={hiddenText}>
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
              <div>
                {data.keySet ? "Patterns are shown as raw text." : "Patterns are shown as raw text. Set OPENROUTER_API_KEY to get readable names."}
              </div>
              <button className="btn sm ghost" onClick={() => setPreviewOpen(true)}>
                {data.keySet ? "Name patterns" : "Preview what would be sent"}
              </button>
            </div>
          )}

          <StartHere data={data} items={startHere(data, filtered)} colorOf={colorOf} labelOf={labelOf} onOpen={(p) => select(p.id)} onInvestigate={(p) => openInvestigate(p)} />

          <LogsVolume
            data={data}
            top={volume.top}
            rest={volume.rest}
            colorOf={colorOf}
            labelOf={labelOf}
            highlight={hoverId}
            onOpen={(id) => select(id)}
            onChooseTimeColumn={async (column) => {
              try {
                const resp = await getJSON<PatternsResponse>(
                  `/api/demo/time-column?ws=${encodeURIComponent(wsId)}${compareTo ? `&compare=${encodeURIComponent(compareTo)}` : ""}`,
                  { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ column }) }
                );
                setData(resp);
                if (!resp.time.columnUnreadable && resp.bucketSeconds > 0) toast(`Using ${column} as time. lapp will remember it for ${service}.`);
              } catch (e) {
                toast(e instanceof Error ? e.message : String(e));
              }
            }}
          />

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
                <option value="rarest" title="Fewest lines first. Rare patterns are often where problems hide.">
                  Rarest first
                </option>
              </select>
            </label>
          </div>

          <section className="table card" role="table">
            <div className="thead" role="row">
              <span>Pattern</span>
              <span className="col-level">Level</span>
              <span>{hasTrend ? `Trend (${unitName(data.bucketSeconds) === "hour" ? "hourly" : `per ${unitName(data.bucketSeconds)}`})` : "Trend"}</span>
              <span>Lines · Share</span>
              <span />
              <span />
            </div>
            <div className="group-h">
              To review · {visible.length} patterns · {fmtCount(left)} lines
            </div>
            {visible.length === 0 && (
              <div className="emptyall">
                <VolumeX className="ico" />
                Everything here is hidden as noise.
                <button
                  className="btn sm"
                  onClick={() => {
                    setShowFiltered(true);
                    window.setTimeout(() => filteredGroupRef.current?.scrollIntoView({ behavior: "smooth" }), 0);
                  }}
                >
                  Show hidden patterns
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
                labelOf={labelOf}
                hidden={false}
                leaving={leaving.has(p.id)}
                flashKey={flash?.id === p.id ? flash.key : undefined}
                selected={selected !== "unmatched" && selected?.id === p.id}
                hovered={hoverId === p.id}
                onDecide={(d) => decide([p.id], d)}
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
              <div className="cell-main">
                <div className="pname">
                  <i className="dot" style={{ background: "var(--muted)" }} />
                  <b>Unmatched lines</b>
                </div>
                <div className="ptpl">Lines that fit no pattern are listed here and are never hidden.</div>
              </div>
              <span className="col-level" />
              <div className="cell-trend">
                {hasTrend && data.unmatched.count > 0 && <Spark buckets={data.unmatched.buckets} color="var(--muted)" label="Unmatched lines" unit={data.bucketSeconds} />}
              </div>
              <div className="cell-count tnum">
                <b>{fmtCount(data.unmatched.count)}</b>
                <span>{formatPercent(data.unmatched.count, total)}</span>
              </div>
              <span />
              <span className="cell-chev">{data.unmatched.count > 0 && <ChevronRight className="ico" />}</span>
            </div>
            {hidden.length > 0 && (
              <>
                <button ref={filteredGroupRef} className="group-h filt grouptoggle" onClick={() => setShowFiltered(!filteredOpen)} aria-expanded={filteredOpen}>
                  {filteredOpen ? <ChevronDown className="ico" /> : <ChevronRight className="ico" />} Hidden as noise · {countOf(hidden.length, "pattern")} · {countOf(filteredLines, "line")}
                </button>
                {filteredOpen &&
                  hiddenRows.map((p) => (
                    <Row
                      key={p.id}
                      p={p}
                      data={data}
                      total={total}
                      color={colorOf(p)}
                labelOf={labelOf}
                      hidden
                      hovered={hoverId === p.id}
                      selected={selected !== "unmatched" && selected?.id === p.id}
                      onDecide={(d) => decide([p.id], d)}
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
              labelOf={labelOf}
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
      </ValueHoverProvider>
      {previewOpen && (
        <PreviewDialog
          wsId={wsId}
          service={data.workspace.service}
          onClose={() => setPreviewOpen(false)}
          onSent={() => {
            setPreviewOpen(false);
            toast("Naming patterns…");
            const poll = window.setInterval(async () => {
              const st = await getJSON<ImportStatus>(`/api/demo/status?ws=${encodeURIComponent(wsId)}`).catch(() => null);
              if (st && (st.state === "succeeded" || st.state === "failed")) {
                window.clearInterval(poll);
                loaded.current = true;
                setReload((n) => n + 1);
                if (st.state === "failed") toast(`Couldn't name patterns: ${st.error ?? "unknown error"}`);
              }
            }, 1000);
          }}
        />
      )}
      {toastNode}
    </>
  );
}

// Unnamed display names drop a leading "[error]" marker; it never contains
// <*>, so field positions line up with the template.
const levelOffset = (p: DemoPattern) => (p.template.split("<*>").length - p.name.split("<*>").length);

// "Start here": the server ranks every candidate (startHere in summary.go);
// the page drops hidden patterns and shows the first 5.
function startHere(data: PatternsResponse, hidden: Set<string>) {
  const byId = new Map(data.patterns.map((p) => [p.id, p]));
  return data.startHere
    .filter((c) => !hidden.has(c.id))
    .slice(0, 5)
    .flatMap((c) => byId.get(c.id) ?? []);
}

function startReason(p: DemoPattern, data: PatternsResponse) {
  return data.startHere.find((c) => c.id === p.id)?.reason;
}

function StartHere({
  data,
  items,
  colorOf,
  labelOf,
  onOpen,
  onInvestigate
}: {
  data: PatternsResponse;
  items: DemoPattern[];
  colorOf: (p: DemoPattern) => string;
  labelOf: LabelOf;
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
        items.map((p) => {
          const label = labelOf(p, NAME_ROW);
          return (
          <div key={p.id} className="sh-row" role="button" tabIndex={0} onClick={() => onOpen(p)} onKeyDown={(e) => e.key === "Enter" && onOpen(p)}>
            <i className="dot" style={{ background: colorOf(p) }} />
            <div className="sh-main">
              {p.named ? (
                <b className="ell" title={p.name}>
                  {label.text}
                </b>
              ) : (
                <b className="unnamed ell" title={p.name}>
                  <Template text={label.text} patternId={p.id} offset={levelOffset(p) + label.fieldOffset} />
                </b>
              )}
              <span className={`sh-why ${p.spikeAt !== undefined || p.sinceLast === "up" ? "is-up" : p.steadyError ? "is-err" : "is-new"}`}>
                {p.spikeAt !== undefined || p.sinceLast === "up" ? (
                  <TrendingUp className="ico" />
                ) : p.steadyError ? (
                  <OctagonAlert className="ico" />
                ) : (
                  <Sparkles className="ico" />
                )}
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
          );
        })
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

// Renders a template with <*> chips. With patternId (and the template being
// the pattern's own text), hovering a chip shows the values it stands for.
function Template({ text, active, patternId, offset = 0 }: { text: string; active?: number; patternId?: string; offset?: number }) {
  const parts = text.split("<*>");
  return (
    <>
      {parts.map((part, i) => (
        <React.Fragment key={i}>
          {part}
          {i < parts.length - 1 && <Placeholder patternId={patternId} index={i + offset} active={active === i} />}
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
  labelOf,
  hidden: filtered,
  leaving,
  flashKey,
  selected,
  hovered,
  onDecide,
  onOpen
}: {
  p: DemoPattern;
  data: PatternsResponse;
  total: number;
  color: string;
  labelOf: LabelOf;
  hidden: boolean;
  leaving?: boolean;
  flashKey?: number;
  selected: boolean;
  hovered?: boolean;
  onDecide: (d: NoiseDecision) => void;
  onOpen: () => void;
}) {
  const share = formatPercent(p.count, total);
  const label = labelOf(p, NAME_ROW);
  const hasTrend = data.bucketSeconds > 0;
  const noise = p.suggestion === "noise";
  const noiseTag = noise && (
    <span className="badge b-noise" title={`Repeats often${p.level === "warn" || p.level === "warning" ? " (a warning, not an error)" : ""}: ${fmtCount(p.count)} lines, ${share} of this log.`}>
      <EyeOff className="ico" />
      Likely noise
    </span>
  );
  return (
    <div
      key={flashKey}
      data-pattern={p.id}
      className={`prow ${filtered ? "filtered" : ""} ${selected ? "selected" : ""} ${hovered ? "hovered" : ""} ${leaving ? "leaving" : ""} ${flashKey ? "flash" : ""}`}
      role="row"
      tabIndex={0}
      onClick={onOpen}
      onKeyDown={(e) => {
        if (e.target !== e.currentTarget) return;
        if (e.key === "Enter") onOpen();
        if (e.key === " ") {
          e.preventDefault();
          onDecide(filtered ? "unhide" : "hide");
        }
      }}
    >
      <div className="cell-main">
        {!p.named && (
          <div className="pname nowrap">
            <i className="dot" style={{ background: color }} />
            {noiseTag}
            <b className="unnamed ell" title="Not named yet — add an OpenRouter key to get readable names">
              <Template text={label.text} patternId={p.id} offset={levelOffset(p) + label.fieldOffset} />
            </b>
          </div>
        )}
        <div className={`pname ${p.named ? "" : "badges-only"}`}>
          {p.named && (
            <>
              <i className="dot" style={{ background: color }} />
              {noiseTag}
              <b title={p.name}>{label.text}</b>
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
          {!filtered && p.newAt !== undefined && p.sinceLast !== "new" && (
            <span className="badge b-new">
              <Sparkles className="ico" />
              First seen {bucketLabel(data, p.newAt)}
            </span>
          )}
          {!filtered && p.sinceLast === "new" && (
            <span className="badge b-new">
              <Sparkles className="ico" />
              New since last time{p.newAt !== undefined ? ` · first seen ${bucketLabel(data, p.newAt)}` : ""}
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
            <Template text={p.template} patternId={p.id} />
          </div>
        )}
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
            {p.ratio}× last time
          </em>
        )}
      </div>
      <div className={`cell-act ${noise || filtered ? "pinned" : ""}`} onClick={(e) => e.stopPropagation()}>
        {filtered ? (
          <button className="linkbtn" onClick={() => onDecide("unhide")}>
            Unhide
          </button>
        ) : (
          <>
            <button className="btn xs" onClick={() => onDecide("hide")}>
              Hide
            </button>
            {noise && (
              <button className="linkbtn" onClick={() => onDecide("not-noise")}>
                Not noise
              </button>
            )}
          </>
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

// ---------- logs volume ----------

const VOLUME_COLLAPSED_KEY = "lapp-logs-volume-collapsed";
const VOLUME_TOP = 8;
// The volume chart draws the top VOLUME_TOP visible patterns (by lines, as the
// server sorts them) as their own series and sums the rest into "Other". This
// split is also the one source of which patterns show their own color.
function volumeSplit(visible: DemoPattern[]) {
  return { top: visible.slice(0, VOLUME_TOP), rest: visible.slice(VOLUME_TOP) };
}
// "Other" in the volume chart is darker than --s-other so it reads as a sum, not a pattern.
const VOLUME_OTHER = "var(--ink-3)";
const VOLUME_OTHER_ID = "~other";
const VOLUME_UNMATCHED_ID = "~unmatched";
// Candidate x-axis tick steps in seconds; the smallest that leaves room for a label wins.
const TICK_STEPS = [60, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400, 2 * 86400, 7 * 86400, 14 * 86400, 30 * 86400];
// Wide enough for "Oct 26" plus a gap, so neighbouring labels never touch.
const TICK_MIN_PX = 64;
// Tooltip distance from the cursor; in the right TIP_FLIP share of the chart it goes left of the cursor.
const TIP_GAP = 12;
const TIP_FLIP = 0.6;

// legend and tip are the series' labels in the legend and the tooltip (labelOf).
type VolumeSeries = { id: string; name: string; color: string; buckets: number[]; other?: boolean; hatched?: boolean; legend: string; tip: string };

// Swatch for legends and tooltips: the same 45° hatch as the bars for unmatched lines.
const swatchStyle = (s: VolumeSeries): React.CSSProperties =>
  s.hatched
    ? { background: "repeating-linear-gradient(45deg, var(--muted) 0 1.5px, var(--surface-2) 1.5px 5.5px)" }
    : { background: s.color };

// Y-axis ticks: 0, the middle and the top, all whole numbers (line counts
// are integers, so "2.5" would read as a bug).
function axisTicks(max: number) {
  return [...new Set([0, Math.ceil(max / 2), max])];
}
// Axis numbers: 1000 → "1K", 1500 → "1.5K".
function fmtAxis(v: number) {
  if (v >= 1e6) return `${+(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${+(v / 1e3).toFixed(1)}K`;
  return Number.isInteger(v) ? String(v) : String(+v.toFixed(2));
}

// Top patterns (volumeSplit) keep their list color; unmatched lines and the remaining patterns follow.
function volumeSeries(
  data: PatternsResponse,
  top: DemoPattern[],
  rest: DemoPattern[],
  colorOf: (p: DemoPattern) => string,
  labelOf: LabelOf
): VolumeSeries[] {
  const series: VolumeSeries[] = top.map((p) => ({
    id: p.id,
    name: p.name,
    color: colorOf(p),
    buckets: p.buckets,
    legend: labelOf(p, NAME_LEGEND).text,
    tip: labelOf(p, NAME_TIP).text
  }));
  // Unmatched lines are hatched so they never read as one more pattern.
  if (data.unmatched.count > 0) {
    const name = "Unmatched lines";
    series.push({ id: VOLUME_UNMATCHED_ID, name, color: "url(#vol-unmatched-hatch)", buckets: data.unmatched.buckets, hatched: true, legend: name, tip: name });
  }
  if (rest.length > 0) {
    const n = data.unmatched.buckets.length;
    const buckets = Array.from({ length: n }, (_, i) => rest.reduce((s, p) => s + p.buckets[i], 0));
    const name = `Other (${countOf(rest.length, "pattern")})`;
    series.push({ id: VOLUME_OTHER_ID, name, color: VOLUME_OTHER, buckets, other: true, legend: name, tip: name });
  }
  return series;
}

function timeTicks(data: PatternsResponse, n: number, plotW: number) {
  const unit = data.bucketSeconds;
  const start = new Date(data.bucketStart!).getTime() / 1000;
  const pxPerSec = plotW / Math.max(1, n * unit);
  const step = TICK_STEPS.find((s) => s >= unit && s * pxPerSec >= TICK_MIN_PX) ?? TICK_STEPS[TICK_STEPS.length - 1];
  const ticks: { at: number; label: string; day: boolean }[] = [];
  for (let t = Math.ceil(start / step) * step; t < start + n * unit; t += step) {
    const d = new Date(t * 1000);
    const day = t % 86400 === 0;
    ticks.push({ at: (t - start) / unit, label: step >= 86400 || day ? formatDate(d) : `${pad(d.getUTCHours())}:${pad(d.getUTCMinutes())}`, day });
  }
  return ticks;
}

function LogsVolume({
  data,
  top,
  rest,
  colorOf,
  labelOf,
  highlight,
  onOpen,
  onChooseTimeColumn
}: {
  data: PatternsResponse;
  top: DemoPattern[];
  rest: DemoPattern[];
  colorOf: (p: DemoPattern) => string;
  labelOf: LabelOf;
  highlight?: string | null;
  onOpen: (id: string) => void;
  onChooseTimeColumn: (column: string) => Promise<void>;
}) {
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(VOLUME_COLLAPSED_KEY) === "1");
  const toggle = () =>
    setCollapsed((c) => {
      localStorage.setItem(VOLUME_COLLAPSED_KEY, c ? "0" : "1");
      return !c;
    });
  const [legendHover, setLegendHover] = useState<string | null>(null);
  const hasTrend = data.bucketSeconds > 0;
  const series = hasTrend ? volumeSeries(data, top, rest, colorOf, labelOf) : [];
  return (
    <section className={`card overview${collapsed ? " is-collapsed" : ""}`}>
      <header>
        <button className="vol-toggle" aria-expanded={!collapsed} aria-label={collapsed ? "Show logs volume" : "Hide logs volume"} onClick={toggle}>
          <ChevronDown className="ico" />
        </button>
        <h2>Logs volume</h2>
        <span className="sub tnum">{hasTrend
            ? `Per ${unitName(data.bucketSeconds)}${data.time.timed < data.time.lines ? ` · ${fmtCount(data.time.timed)} of ${countOf(data.time.lines, "line")} have a time` : ""} · hidden noise not shown`
            : ""}</span>
        <div style={{ flex: 1 }} />
        {hasTrend && !collapsed && (
          <div className="legend">
            {series.map((s) => (
              <span key={s.id} title={s.name} onMouseEnter={() => setLegendHover(s.id)} onMouseLeave={() => setLegendHover(null)}>
                <i style={swatchStyle(s)} />
                {s.legend}
              </span>
            ))}
          </div>
        )}
      </header>
      {!collapsed &&
        (hasTrend ? (
          <Overview data={data} series={series} highlight={legendHover ?? highlight} onOpen={(id) => onOpen(id === VOLUME_UNMATCHED_ID ? "unmatched" : id)} />
        ) : (
          <NoTimes data={data} onChoose={onChooseTimeColumn} />
        ))}
    </section>
  );
}

function Overview({ data, series, highlight, onOpen }: { data: PatternsResponse; series: VolumeSeries[]; highlight?: string | null; onOpen: (id: string) => void }) {
  const [ref, W] = useWidth<HTMLDivElement>(1000);
  const [hover, setHover] = useState<{ i: number; id: string | null } | null>(null);
  const n = data.unmatched.buckets.length;
  const totals = Array.from({ length: n }, (_, i) => series.reduce((s, x) => s + x.buckets[i], 0));
  const maxV = niceMax(Math.max(1, ...totals));
  const H = 160;
  const top = 6;
  const padL = 40;
  const bw = (W - padL) / Math.max(n, 1);
  const gap = bw > 4 ? 2 : 0;
  const y = (v: number) => top + H - (v / maxV) * H;
  const ticks = timeTicks(data, n, W - padL);
  const [pt, setPt] = useState<{ x: number; y: number } | null>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  // Places the tooltip after it renders, when its size is known: beside the
  // cursor, and never outside the chart card vertically.
  useLayoutEffect(() => {
    const tip = tipRef.current;
    const wrap = ref.current;
    if (!tip || !wrap || !pt) return;
    const w = wrap.getBoundingClientRect();
    const c = (wrap.closest(".card") ?? wrap).getBoundingClientRect();
    const minTop = c.top - w.top + 4;
    const maxBottom = c.bottom - w.top - 4;
    tip.style.maxHeight = `${maxBottom - minTop}px`;
    const left = pt.x > W * TIP_FLIP ? pt.x - TIP_GAP - tip.offsetWidth : pt.x + TIP_GAP;
    tip.style.left = `${Math.max(0, Math.min(left, W - tip.offsetWidth))}px`;
    tip.style.top = `${Math.max(minTop, Math.min(8, maxBottom - tip.offsetHeight))}px`;
  });
  return (
    <div
      className="chart-wrap"
      ref={ref}
      onMouseMove={(e) => {
        const r = e.currentTarget.getBoundingClientRect();
        setPt({ x: e.clientX - r.left, y: e.clientY - r.top });
      }}
      onMouseLeave={() => {
        setHover(null);
        setPt(null);
      }}
    >
      <svg width={W} height={H + top + 22} role="img" aria-label="Logs volume over time, stacked by pattern">
        <defs>
          <pattern id="vol-unmatched-hatch" width="4" height="4" patternUnits="userSpaceOnUse" patternTransform="rotate(45)">
            <rect width="4" height="4" fill="var(--surface-2)" />
            <line x1="0" y1="0" x2="0" y2="4" stroke="var(--muted)" strokeWidth="1.5" />
          </pattern>
        </defs>
        {axisTicks(maxV).map((v) => (
          <g key={v}>
            <line x1={padL} x2={W} y1={y(v)} y2={y(v)} stroke="var(--line)" />
            <text x={padL - 8} y={y(v) + 4} textAnchor="end" className="axis">
              {fmtAxis(v)}
            </text>
          </g>
        ))}
        {totals.map((_, i) => {
          let acc = 0;
          const x = padL + i * bw;
          return (
            <g key={i} onMouseEnter={() => setHover({ i, id: null })}>
              <rect x={x} y={top} width={bw} height={H} fill={hover?.i === i ? "var(--ring)" : "transparent"} />
              {series.map((s) => {
                const v = s.buckets[i];
                if (!v) return null;
                const y0 = y(acc);
                // A 1px --surface gap above the segment below, so two neighbours never merge.
                const seam = acc > 0 && y0 - y(acc + v) >= 2 ? 1 : 0;
                acc += v;
                return (
                  <React.Fragment key={s.id}>
                    <rect
                      className={s.other ? undefined : "vol-seg"}
                      x={x + gap / 2}
                      width={Math.max(1, bw - gap)}
                      y={y(acc)}
                      height={Math.max(0.5, y0 - y(acc) - seam)}
                      fill={s.color}
                      opacity={highlight && highlight !== s.id ? 0.3 : 1}
                      onMouseEnter={() => setHover({ i, id: s.id })}
                      onMouseLeave={() => setHover({ i, id: null })}
                      onClick={s.other ? undefined : () => onOpen(s.id)}
                    />
                    {seam > 0 && <rect x={x + gap / 2} width={Math.max(1, bw - gap)} y={y0 - seam} height={seam} fill="var(--surface)" pointerEvents="none" />}
                  </React.Fragment>
                );
              })}
            </g>
          );
        })}
        {ticks.map((t) => {
          const x = padL + t.at * bw;
          return (
            <g key={t.at}>
              <line x1={x} x2={x} y1={top + H} y2={top + H + 4} stroke="var(--line-strong)" />
              <text x={x} y={H + top + 16} textAnchor={x < padL + 16 ? "start" : x > W - 20 ? "end" : "middle"} className={t.day ? "axis lbl" : "axis"}>
                {t.label}
              </text>
            </g>
          );
        })}
      </svg>
      {hover !== null && totals[hover.i] > 0 && (
        <div ref={tipRef} className="tooltip tnum" style={{ top: 8, overflow: "hidden", pointerEvents: "none" }}>
          <div className="tt-h">
            {data.bucketSeconds >= 86400 ? bucketLabel(data, hover.i) : `${bucketLabel(data, hover.i)} – ${bucketLabel(data, hover.i + 1).split(" ").pop()}`}
          </div>
          {series
            .filter((s) => s.buckets[hover.i] > 0)
            .sort((a, b) => Number(!!a.other) - Number(!!b.other) || b.buckets[hover.i] - a.buckets[hover.i])
            .map((s) => (
              <div key={s.id} className={`tt-row${hover.id === s.id ? " is-on" : ""}`}>
                <i style={swatchStyle(s)} />
                <span>{s.tip}</span>
                <b>{fmtCount(s.buckets[hover.i])}</b>
              </div>
            ))}
          <div className="tt-f">Total {countOf(totals[hover.i], "line")}</div>
        </div>
      )}
    </div>
  );
}

// Says why there is no volume chart; for CSV and JSON files without a recognized time,
// lets the user pick the time column.
function NoTimes({ data, onChoose }: { data: PatternsResponse; onChoose: (column: string) => Promise<void> }) {
  const t = data.time;
  const [busy, setBusy] = useState<string | null>(null);
  const partial = t.timed > 0;
  // The server lists columns only when no time was recognized on its own.
  const canPick = (t.format === "csv" || t.format === "json") && (t.columns?.length ?? 0) > 0;
  let reason: React.ReactNode;
  if (partial) {
    reason = `Only ${fmtCount(t.timed)} of ${countOf(t.lines, "line")} ${t.timed === 1 ? "has" : "have"} a time, too few to draw a chart.`;
  } else if (t.format === "json") {
    reason = `None of the JSON lines have a time field lapp looks for (${(t.fields ?? []).join(", ")}).${canPick ? " Pick the time field below." : ""}`;
  } else if (t.format === "csv" && canPick) {
    reason = "None of the columns look like a time lapp can read. Pick the time column below.";
  } else {
    reason = (
      <>
        None of the lines start with a time lapp can read, like <span className="mono">2026-10-26 05:41:47</span> or{" "}
        <span className="mono">Oct 26 05:41:47</span>.
      </>
    );
  }
  const choose = async (column: string) => {
    setBusy(column);
    try {
      await onChoose(column);
    } finally {
      setBusy(null);
    }
  };
  const bad = !busy && t.columnUnreadable && !!t.column;
  return (
    <div className="notime-chart">
      <Clock className="ico" />
      <div className="notime-text">
        <div className="notime-main">No timestamps found, so there's no volume chart. Line counts are still below.</div>
        <div className="sub">{reason}</div>
        {canPick && (
          <div className="timecol">
            <label className="sel">
              <span>Time column</span>
              <select className={bad ? "is-bad" : undefined} value={busy ?? t.column ?? ""} disabled={!!busy} onChange={(e) => choose(e.target.value)}>
                <option value="" disabled>
                  Choose…
                </option>
                {t.columns!.map((c) => (
                  <option key={c} value={c}>
                    {c}
                  </option>
                ))}
              </select>
            </label>
            {busy && <LoaderCircle className="ico spin" aria-label="Reading times" />}
          </div>
        )}
        {canPick && bad && (
          <div className="timecol-err">
            <OctagonAlert className="ico" /> Couldn't read times in {t.column}. Try another column.
          </div>
        )}
      </div>
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
          <Template text={target.template} patternId={target.id} />
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
              Hide this pattern
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
          {axisTicks(maxV).map((v) => (
            <g key={v}>
              <line x1={padL} x2={W} y1={y(v)} y2={y(v)} stroke="var(--line)" />
              <text x={padL - 6} y={y(v) + 4} textAnchor="end" className="axis">
                {fmtNum(v)}
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
