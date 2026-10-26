import React, { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Check, ChevronDown, ChevronRight, Clock, Copy, Download, Plus, Search, Sparkles, TrendingUp, Upload, VolumeX, X } from "lucide-react";
import "./demo.css";

type DemoWorkspace = {
  id: string;
  title: string;
  service: string;
  lineCount: number;
  patternCount: number;
  updatedAt: string;
};

type DemoPattern = {
  id: string;
  name: string;
  named: boolean;
  template: string;
  count: number;
  level: string;
  buckets: number[];
  suggestion: "noise" | "maybe" | "";
  reason: string;
  filtered: boolean;
  spikeAt?: number;
  newAt?: number;
  samples: string[];
};

type PatternsResponse = {
  workspace: DemoWorkspace;
  fileNames: string[];
  from?: string;
  to?: string;
  bucketStart?: string;
  bucketSeconds: number;
  rulesSaved: boolean;
  named: boolean;
  patterns: DemoPattern[];
  unmatched: { count: number; buckets: number[]; samples: string[] };
};

const PALETTE = ["#1f9d6b", "#e8a20c", "#e26d96", "#16801f", "#3b7dd8", "#8a5cd6", "#d4582a", "#14a3b8"];
const GRAY = "#b4bcc6";

// formatPercent is the single place percentages are rounded: 1 decimal, half up.
export function formatPercent(part: number, total: number) {
  if (total === 0) return "0.0%";
  return `${(Math.round((part / total) * 1000) / 10).toFixed(1)}%`;
}

function formatCount(n: number) {
  return n.toLocaleString("en-US");
}

function bucketLabel(data: PatternsResponse, index: number) {
  if (!data.bucketStart) return "";
  const t = new Date(new Date(data.bucketStart).getTime() + index * data.bucketSeconds * 1000);
  return formatTime(t);
}

function formatTime(t: Date) {
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(t.getUTCMonth() + 1)}-${pad(t.getUTCDate())} ${pad(t.getUTCHours())}:${pad(t.getUTCMinutes())}`;
}

function unitLabel(seconds: number) {
  switch (seconds) {
    case 60:
      return "minute";
    case 300:
      return "5 minutes";
    case 900:
      return "15 minutes";
    case 3600:
      return "hour";
    case 21600:
      return "6 hours";
    case 86400:
      return "day";
    case 604800:
      return "week";
    default:
      return "month";
  }
}

function perUnit(seconds: number) {
  const unit = unitLabel(seconds);
  return /^\d/.test(unit) ? `per ${unit}` : `/${unit}`;
}

async function getJSON<T>(url: string, init?: RequestInit): Promise<T> {
  const res = await fetch(url, init);
  const body = await res.json();
  if (!res.ok) throw new Error(body?.error ?? res.statusText);
  return body as T;
}

export function DemoApp() {
  const [wsId, setWsId] = useState(() => new URLSearchParams(window.location.search).get("ws") ?? "");
  useEffect(() => {
    const onPop = () => setWsId(new URLSearchParams(window.location.search).get("ws") ?? "");
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);
  const open = useCallback((id: string) => {
    const url = id ? `?ws=${encodeURIComponent(id)}` : window.location.pathname;
    window.history.pushState(null, "", url);
    setWsId(id);
  }, []);
  return wsId ? <PatternList wsId={wsId} onOpen={open} /> : <ImportPage onOpen={open} />;
}

function TopBar({ crumb, onOpen }: { crumb?: React.ReactNode; onOpen: (id: string) => void }) {
  const [recentOpen, setRecentOpen] = useState(false);
  const [recent, setRecent] = useState<DemoWorkspace[]>([]);
  useEffect(() => {
    if (recentOpen) getJSON<DemoWorkspace[]>("/api/demo/workspaces").then(setRecent).catch(() => setRecent([]));
  }, [recentOpen]);
  return (
    <header className="d-topbar">
      <button className="d-brand" onClick={() => onOpen("")}>
        <span className="d-logo">L</span> lapp
      </button>
      {crumb}
      <div className="d-topbar-right">
        <div className="d-recent">
          <button className="d-ghost" onClick={() => setRecentOpen((v) => !v)}>
            <Clock size={16} /> Recent
          </button>
          {recentOpen && (
            <div className="d-menu" onMouseLeave={() => setRecentOpen(false)}>
              {recent.length === 0 && <div className="d-menu-empty">No imports yet</div>}
              {recent.map((w) => (
                <button key={w.id} className="d-menu-item" onClick={() => { setRecentOpen(false); onOpen(w.id); }}>
                  <strong>{w.service}</strong> <span>{w.title}</span> <em>{formatCount(w.lineCount)} lines</em>
                </button>
              ))}
            </div>
          )}
        </div>
        <button className="d-outline" onClick={() => onOpen("")}>
          <Plus size={16} /> Import logs
        </button>
      </div>
    </header>
  );
}

function ImportPage({ onOpen }: { onOpen: (id: string) => void }) {
  const [state, setState] = useState<"idle" | "working" | "failed">("idle");
  const [error, setError] = useState("");
  const [dragging, setDragging] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);

  const upload = async (file: File) => {
    setState("working");
    try {
      const res = await getJSON<{ id: string }>(`/api/demo/import?name=${encodeURIComponent(file.name)}`, {
        method: "POST",
        body: file
      });
      onOpen(res.id);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setState("failed");
    }
  };

  return (
    <div className="d-page">
      <TopBar onOpen={onOpen} />
      <main className="d-import">
        <h1>Import logs</h1>
        <div
          className={`d-drop ${dragging ? "is-dragging" : ""}`}
          onClick={() => inputRef.current?.click()}
          onDragOver={(e) => { e.preventDefault(); setDragging(true); }}
          onDragLeave={() => setDragging(false)}
          onDrop={(e) => {
            e.preventDefault();
            setDragging(false);
            const f = e.dataTransfer.files[0];
            if (f) upload(f);
          }}
        >
          <Upload size={28} />
          {state === "working" ? <p>Finding patterns…</p> : <p>Drop a log file here, or click to choose one</p>}
          <input ref={inputRef} type="file" hidden onChange={(e) => e.target.files?.[0] && upload(e.target.files[0])} />
        </div>
        {state === "failed" && <p className="d-error">Couldn't read this file. {error}</p>}
        <p className="d-privacy">
          To name patterns, lapp sends a few sample lines from each pattern to OpenRouter. Everything else stays on this machine.
        </p>
        <p className="d-hint">
          From a terminal: <code>lapp app.log</code> or <code>kubectl logs my-pod | lapp</code>
        </p>
      </main>
    </div>
  );
}

function PatternList({ wsId, onOpen }: { wsId: string; onOpen: (id: string) => void }) {
  const [data, setData] = useState<PatternsResponse | null>(null);
  const [error, setError] = useState("");
  const [filtered, setFiltered] = useState<Set<string>>(new Set());
  const [query, setQuery] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);
  const [sort, setSort] = useState<"lines" | "spiking" | "new">("lines");
  const [showFiltered, setShowFiltered] = useState(true);
  const [detail, setDetail] = useState<DemoPattern | "unmatched" | null>(null);
  const [savedNote, setSavedNote] = useState(false);
  const [bannerOpen, setBannerOpen] = useState(true);

  useEffect(() => {
    setData(null);
    getJSON<PatternsResponse>(`/api/demo/patterns?ws=${encodeURIComponent(wsId)}`)
      .then((d) => {
        setData(d);
        setFiltered(new Set(d.patterns.filter((p) => p.filtered).map((p) => p.id)));
        setBannerOpen(!d.rulesSaved);
      })
      .catch((e) => setError(e instanceof Error ? e.message : String(e)));
  }, [wsId]);

  // Colors follow rank by total lines and never change when filtering.
  const colors = useMemo(() => {
    const m = new Map<string, string>();
    data?.patterns.forEach((p, i) => m.set(p.id, i < PALETTE.length ? PALETTE[i] : GRAY));
    return m;
  }, [data]);

  if (error) {
    return (
      <div className="d-page">
        <TopBar onOpen={onOpen} />
        <p className="d-error d-pad">{error}</p>
      </div>
    );
  }
  if (!data) {
    return (
      <div className="d-page">
        <TopBar onOpen={onOpen} />
        <p className="d-pad d-muted">Loading patterns…</p>
      </div>
    );
  }

  const total = data.workspace.lineCount;
  const filteredLines = data.patterns.filter((p) => filtered.has(p.id)).reduce((s, p) => s + p.count, 0);
  const left = total - filteredLines;
  const visible = data.patterns.filter((p) => !filtered.has(p.id));
  const hidden = data.patterns.filter((p) => filtered.has(p.id));
  const autoNoise = data.patterns.filter((p) => p.suggestion === "noise");
  const autoNoiseLines = autoNoise.reduce((s, p) => s + p.count, 0);
  const hasTrend = data.bucketSeconds > 0;
  const unit = unitLabel(data.bucketSeconds);

  const matches = (p: DemoPattern) => {
    if (errorsOnly && !["error", "warn", "fatal"].includes(p.level)) return false;
    if (!query) return true;
    const q = query.toLowerCase();
    return p.name.toLowerCase().includes(q) || p.template.toLowerCase().includes(q);
  };
  const sorter = (a: DemoPattern, b: DemoPattern) => {
    if (sort === "spiking") return Number(b.spikeAt !== undefined) - Number(a.spikeAt !== undefined) || b.count - a.count;
    if (sort === "new") return (b.newAt ?? -1) - (a.newAt ?? -1) || b.count - a.count;
    return b.count - a.count;
  };

  const toggle = (id: string) => {
    setSavedNote(false);
    setFiltered((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const save = async () => {
    await getJSON(`/api/demo/rules?ws=${encodeURIComponent(wsId)}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ filtered: [...filtered] })
    });
    setSavedNote(true);
    setBannerOpen(false);
  };

  const exportHref =
    `/api/demo/export?ws=${encodeURIComponent(wsId)}` +
    (filtered.size === 0 ? "&filtered=" : [...filtered].map((id) => `&filtered=${encodeURIComponent(id)}`).join(""));
  const fileName = data.fileNames[0] ?? data.workspace.title;

  return (
    <div className="d-page">
      <TopBar
        onOpen={onOpen}
        crumb={
          <div className="d-crumb">
            <span className="d-slash">/</span>
            <strong>{data.workspace.service}</strong>
            <span className="d-muted">
              {fileName} · {formatCount(total)} lines
              {data.from && data.to ? ` · ${formatTime(new Date(data.from))} – ${formatTime(new Date(data.to))}` : ""}
            </span>
          </div>
        }
      />
      <main className="d-main">
        <section className="d-summary">
          <div>
            <div className="d-summary-line">
              <span className="d-big">{formatPercent(filteredLines, total)} filtered</span>
              <span className="d-muted">
                <b>{formatCount(left)}</b> lines left · <b>{visible.length}</b> patterns · <b>{formatCount(data.unmatched.count)}</b> unmatched lines
              </span>
            </div>
            <div className="d-progress">
              <div style={{ width: formatPercent(filteredLines, total) }} />
            </div>
          </div>
          <div className="d-summary-actions">
            <button className="d-chip" onClick={() => navigator.clipboard?.writeText(`lapp filter ${fileName} > clean.log`)} title="Copy">
              <span className="d-mono">&gt;_ lapp filter {fileName} &gt; clean.log</span> <Copy size={14} />
            </button>
            <a className="d-primary" href={exportHref}>
              <Download size={16} /> Export {formatCount(left)} lines
            </a>
          </div>
        </section>
        {!data.named && <div className="d-keynote">Patterns are shown as raw text. Set OPENROUTER_API_KEY to get readable names.</div>}
        {savedNote && <div className="d-note">Noise rules saved for {data.workspace.service}. They'll apply next time.</div>}

        <section className="d-card d-chart-card">
          <div className="d-card-head">
            <div>
              <strong>Remaining lines over time</strong>{" "}
              <span className="d-muted">{hasTrend ? `Per ${unit} · filtered patterns not shown` : ""}</span>
            </div>
            <div className="d-legend">
              {visible.slice(0, 6).map((p) => (
                <span key={p.id}>
                  <i style={{ background: colors.get(p.id) }} />
                  {shortName(p)}
                </span>
              ))}
            </div>
          </div>
          {hasTrend ? (
            <StackedChart data={data} patterns={visible} colors={colors} />
          ) : (
            <p className="d-muted d-pad">Trend unavailable — no timestamps found in these lines</p>
          )}
        </section>

        {bannerOpen && autoNoise.length > 0 && (
          <section className="d-banner">
            <VolumeX size={18} />
            <span>
              lapp filtered {autoNoise.length} patterns that look like noise ({formatCount(autoNoiseLines)} lines,{" "}
              {formatPercent(autoNoiseLines, total)}). Based on share and repetition, not log level. Save to apply the same rules to{" "}
              <b>{data.workspace.service}</b> logs next time.
            </span>
            <button className="d-primary" onClick={save}>
              <Check size={16} /> Confirm and save
            </button>
            <button className="d-ghost" onClick={() => { setFiltered(new Set()); setSavedNote(false); }}>
              Unfilter all
            </button>
          </section>
        )}

        <section className="d-toolbar">
          <label className="d-search">
            <Search size={16} />
            <input placeholder="Search names or templates" value={query} onChange={(e) => setQuery(e.target.value)} />
          </label>
          <div className="d-seg">
            <button className={!errorsOnly ? "is-on" : ""} onClick={() => setErrorsOnly(false)}>All</button>
            <button className={errorsOnly ? "is-on" : ""} onClick={() => setErrorsOnly(true)}>Errors and warnings only</button>
          </div>
          <div className="d-sort">
            <span className="d-muted">Sort</span>
            <select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)}>
              <option value="lines">Most lines</option>
              <option value="spiking">Spiking</option>
              <option value="new">New</option>
            </select>
            {!bannerOpen && (
              <button className="d-outline" onClick={save}>
                Save noise rules
              </button>
            )}
          </div>
        </section>

        <section className="d-card d-table">
          <div className="d-row d-head">
            <span>Filter</span>
            <span>Pattern</span>
            <span>Level</span>
            <span>{hasTrend ? `Trend (${unit === "hour" ? "hourly" : `per ${unit}`})` : "Trend"}</span>
            <span>Lines · Share</span>
          </div>
          <div className="d-group">
            To review · {visible.length} patterns · {formatCount(left)} lines
          </div>
          {visible.length === 0 && (
            <div className="d-empty">
              Everything here is filtered as noise.{" "}
              <button className="d-link" onClick={() => setShowFiltered(true)}>Show filtered patterns</button>
            </div>
          )}
          {visible.filter(matches).sort(sorter).map((p) => (
            <PatternRow key={p.id} p={p} data={data} total={total} color={colors.get(p.id)!} filtered={false} onToggle={toggle} onOpen={() => setDetail(p)} />
          ))}
          <div className="d-row d-unmatched" onClick={() => setDetail("unmatched")}>
            <span />
            <span>
              <span className="d-name"><i style={{ background: "#8b95a1" }} /> Unmatched lines</span>
              <span className="d-muted d-small">Lines that fit no pattern are listed here and are never filtered.</span>
            </span>
            <span />
            <span>{hasTrend && <Sparkline buckets={data.unmatched.buckets} color="#8b95a1" />}</span>
            <span className="d-count"><b>{formatCount(data.unmatched.count)}</b></span>
          </div>
          {hidden.length > 0 && (
            <>
              <button className="d-group d-group-btn" onClick={() => setShowFiltered((v) => !v)}>
                {showFiltered ? <ChevronDown size={16} /> : <ChevronRight size={16} />} Filtered · {hidden.length} patterns ·{" "}
                {formatCount(filteredLines)} lines
              </button>
              {showFiltered &&
                hidden.filter(matches).sort(sorter).map((p) => (
                  <PatternRow key={p.id} p={p} data={data} total={total} color={GRAY} filtered onToggle={toggle} onOpen={() => setDetail(p)} />
                ))}
            </>
          )}
        </section>
      </main>
      {detail && <Detail wsId={wsId} data={data} target={detail} color={detail === "unmatched" ? "#8b95a1" : colors.get(detail.id)!} onClose={() => setDetail(null)} />}
    </div>
  );
}

function shortName(p: DemoPattern) {
  return p.name.length > 32 ? `${p.name.slice(0, 30)}…` : p.name;
}

function Template({ text }: { text: string }) {
  const parts = text.split("<*>");
  return (
    <span className="d-mono d-template">
      {parts.map((part, i) => (
        <React.Fragment key={i}>
          {part}
          {i < parts.length - 1 && <mark>&lt;*&gt;</mark>}
        </React.Fragment>
      ))}
    </span>
  );
}

function PatternRow({
  p,
  data,
  total,
  color,
  filtered,
  onToggle,
  onOpen
}: {
  p: DemoPattern;
  data: PatternsResponse;
  total: number;
  color: string;
  filtered: boolean;
  onToggle: (id: string) => void;
  onOpen: () => void;
}) {
  const share = formatPercent(p.count, total);
  return (
    <div className={`d-row ${filtered ? "is-filtered" : ""}`} onClick={onOpen}>
      <span onClick={(e) => e.stopPropagation()}>
        <input
          type="checkbox"
          checked={filtered}
          title={filtered ? "Show this pattern" : "Filter this pattern"}
          onChange={() => onToggle(p.id)}
        />
      </span>
      <span className="d-pattern-cell">
        <span className="d-name">
          <i style={{ background: color }} />
          {p.named ? (
            <b>{p.name}</b>
          ) : (
            <b className="d-mono d-unnamed" title="Not named yet — add an OpenRouter key to get readable names">
              <Template text={p.template} />
            </b>
          )}
          {!filtered && p.spikeAt !== undefined && (
            <span className="d-badge d-spike">
              <TrendingUp size={13} /> Spiked to {p.buckets[p.spikeAt]}{perUnit(data.bucketSeconds)} at {bucketLabel(data, p.spikeAt)}
            </span>
          )}
          {!filtered && p.newAt !== undefined && (
            <span className="d-badge d-new">
              <Sparkles size={13} /> First seen {bucketLabel(data, p.newAt)}
            </span>
          )}
          {p.suggestion === "noise" && (
            <span className="d-badge d-noise">
              <VolumeX size={13} /> Likely noise · {p.reason}
            </span>
          )}
          {p.suggestion === "maybe" && !filtered && (
            <span className="d-badge d-maybe" title="Not filtered. Decide for yourself.">
              Maybe noise · {p.reason}
            </span>
          )}
        </span>
        {p.named && <Template text={p.template} />}
      </span>
      <span>{p.level && <span className={`d-level d-level-${p.level}`}>{p.level}</span>}</span>
      <span>{data.bucketSeconds > 0 && <Sparkline buckets={p.buckets} color={color} mark={filtered ? undefined : p.spikeAt} />}</span>
      <span className="d-count">
        <span>
          <b>{formatCount(p.count)}</b>
          <em>{share}</em>
        </span>
        <span className="d-bar">
          <span style={{ width: share, background: color }} />
        </span>
      </span>
    </div>
  );
}

function Sparkline({ buckets, color, mark }: { buckets: number[]; color: string; mark?: number }) {
  const w = 200;
  const h = 36;
  const maxV = Math.max(1, ...buckets);
  const bw = w / Math.max(buckets.length, 1);
  return (
    <svg className="d-spark" viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none">
      {buckets.map((v, i) =>
        v > 0 ? <rect key={i} x={i * bw + bw * 0.15} width={bw * 0.7} y={h - (v / maxV) * (h - 4)} height={(v / maxV) * (h - 4)} fill={color} /> : null
      )}
      {mark !== undefined && <circle cx={mark * bw + bw / 2} cy={h - (buckets[mark] / maxV) * (h - 4)} r={3} fill="#e5484d" />}
      <line x1={0} x2={w} y1={h - 0.5} y2={h - 0.5} stroke="#dfe3e8" />
    </svg>
  );
}

function StackedChart({ data, patterns, colors }: { data: PatternsResponse; patterns: DemoPattern[]; colors: Map<string, string> }) {
  const [hover, setHover] = useState<number | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(1000);
  useEffect(() => {
    const el = boxRef.current;
    if (!el) return;
    const ro = new ResizeObserver(() => setWidth(Math.max(320, el.clientWidth)));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  const n = data.unmatched.buckets.length;
  const series = [...patterns.map((p) => ({ id: p.id, name: p.name, color: colors.get(p.id)!, buckets: p.buckets })), { id: "~unmatched", name: "Unmatched lines", color: "#8b95a1", buckets: data.unmatched.buckets }];
  const totals = Array.from({ length: n }, (_, i) => series.reduce((s, x) => s + x.buckets[i], 0));
  const maxV = niceMax(Math.max(1, ...totals));
  const W = width;
  const H = 180;
  const padL = 36;
  const bw = (W - padL) / Math.max(n, 1);
  const ticks = Math.max(1, Math.ceil(n / Math.max(1, Math.floor((W - padL) / 110))));
  return (
    <div className="d-chart" ref={boxRef}>
      <svg viewBox={`0 0 ${W} ${H + 24}`} width={W} height={H + 24} onMouseLeave={() => setHover(null)}>
        {[0, 0.5, 1].map((f) => (
          <g key={f}>
            <line x1={padL} x2={W} y1={H - f * H} y2={H - f * H} stroke="#e8ebef" />
            <text x={padL - 6} y={H - f * H + (f === 1 ? 12 : 4)} textAnchor="end" className="d-axis">
              {Math.round(maxV * f)}
            </text>
          </g>
        ))}
        {totals.map((_, i) => {
          let y = H;
          return (
            <g key={i} onMouseEnter={() => setHover(i)}>
              <rect x={padL + i * bw} y={0} width={bw} height={H} fill={hover === i ? "#eef1f4" : "transparent"} />
              {series.map((s) => {
                const v = s.buckets[i];
                if (!v) return null;
                const hh = (v / maxV) * H;
                y -= hh;
                return <rect key={s.id} x={padL + i * bw + bw * 0.1} width={bw * 0.8} y={y} height={hh} fill={s.color} />;
              })}
              {i % ticks === 0 && (
                <text x={padL + i * bw} y={H + 18} className="d-axis">
                  {bucketLabel(data, i)}
                </text>
              )}
            </g>
          );
        })}
      </svg>
      {hover !== null && (
        <div className="d-tooltip" style={{ left: `${((padL + (hover + 1) * bw) / W) * 100}%` }}>
          <div className="d-muted">
            {bucketLabel(data, hover)} – {bucketLabel(data, hover + 1).slice(6)}
          </div>
          {series
            .filter((s) => s.buckets[hover] > 0)
            .sort((a, b) => b.buckets[hover] - a.buckets[hover])
            .slice(0, 8)
            .map((s) => (
              <div key={s.id} className="d-tip-row">
                <i style={{ background: s.color }} />
                <span>{s.name.length > 40 ? `${s.name.slice(0, 38)}…` : s.name}</span>
                <b>{s.buckets[hover]}</b>
              </div>
            ))}
          <div className="d-tip-total">Total {totals[hover]} lines</div>
        </div>
      )}
    </div>
  );
}

function niceMax(v: number) {
  const p = Math.pow(10, Math.floor(Math.log10(v)));
  for (const m of [1, 2, 5, 10]) if (m * p >= v) return m * p;
  return 10 * p;
}

function Detail({
  wsId,
  data,
  target,
  color,
  onClose
}: {
  wsId: string;
  data: PatternsResponse;
  target: DemoPattern | "unmatched";
  color: string;
  onClose: () => void;
}) {
  const [lines, setLines] = useState<string[] | null>(null);
  const isUnmatched = target === "unmatched";
  useEffect(() => {
    const q = isUnmatched ? "" : `&pattern=${encodeURIComponent(target.id)}`;
    getJSON<string[]>(`/api/demo/lines?ws=${encodeURIComponent(wsId)}${q}`).then(setLines).catch(() => setLines([]));
  }, [wsId, target, isUnmatched]);
  const buckets = isUnmatched ? data.unmatched.buckets : target.buckets;
  const count = isUnmatched ? data.unmatched.count : target.count;
  return (
    <div className="d-drawer-backdrop" onClick={onClose}>
      <aside className="d-drawer" onClick={(e) => e.stopPropagation()}>
        <header>
          <h2>
            <i style={{ background: color }} />
            {isUnmatched ? "Unmatched lines" : target.name}
          </h2>
          <button className="d-ghost" onClick={onClose}>
            <X size={18} />
          </button>
        </header>
        {!isUnmatched && <Template text={target.template} />}
        <h3>Over time</h3>
        {data.bucketSeconds > 0 ? (
          <div className="d-detail-chart">
            <Sparkline buckets={buckets} color={color} mark={isUnmatched ? undefined : target.spikeAt} />
            <div className="d-axis-row">
              <span>{bucketLabel(data, 0)}</span>
              <span>{bucketLabel(data, buckets.length - 1)}</span>
            </div>
          </div>
        ) : (
          <p className="d-muted">Trend unavailable — no timestamps found in these lines</p>
        )}
        <h3>Lines ({formatCount(count)})</h3>
        <pre className="d-lines">{lines === null ? "Loading…" : lines.join("\n")}</pre>
      </aside>
    </div>
  );
}
