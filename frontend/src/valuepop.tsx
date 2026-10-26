import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

type ValueCount = { value: string; count: number };
type FieldSummary = {
  index: number;
  kind: "same" | "values" | "numbers" | "distinct";
  lines: number;
  distinct: number;
  top: ValueCount[];
  overall?: { median: number; p95: number; max: number };
  unit?: string;
};

type Ctx = {
  wsId: string;
  colorOf: (patternId: string) => string;
  onShowChart: (patternId: string, fieldIndex: number) => void;
};

const HoverContext = createContext<Ctx | null>(null);
const cache = new Map<string, Promise<FieldSummary[]>>();

export function ValueHoverProvider({ value, children }: { value: Ctx; children: React.ReactNode }) {
  return <HoverContext.Provider value={value}>{children}</HoverContext.Provider>;
}

function loadSummary(wsId: string, patternId: string) {
  const key = `${wsId}\u0000${patternId}`;
  let p = cache.get(key);
  if (!p) {
    p = fetch(`/api/demo/pattern?ws=${encodeURIComponent(wsId)}&pattern=${encodeURIComponent(patternId)}`)
      .then((r) => (r.ok ? r.json() : { summary: [] }))
      .then((d) => (d.summary ?? []) as FieldSummary[]);
    cache.set(key, p);
  }
  return p;
}

const fmt = (n: number) => n.toLocaleString("en-US");
const pct = (part: number, total: number) => (total ? `${(Math.round((part / total) * 1000) / 10).toFixed(1)}%` : "0.0%");
const num = (x: number) => (Number.isInteger(x) ? String(x) : String(+x.toFixed(2)));

// A <*> chip that shows the values it stands for on hover (300ms) or focus.
export function Placeholder({ patternId, index, active }: { patternId?: string; index: number; active?: boolean }) {
  const ctx = useContext(HoverContext);
  const ref = useRef<HTMLSpanElement>(null);
  const timer = useRef<number | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [data, setData] = useState<FieldSummary | null | undefined>(undefined);
  const interactive = !!(ctx && patternId);

  const show = useCallback(() => {
    if (!ctx || !patternId) return;
    setOpen(true);
    loadSummary(ctx.wsId, patternId).then((s) => setData(s.find((f) => f.index === index + 1) ?? null));
  }, [ctx, patternId, index]);
  const hide = () => {
    window.clearTimeout(timer.current);
    setOpen(false);
  };
  useEffect(() => () => window.clearTimeout(timer.current), []);

  return (
    <>
      <span
        ref={ref}
        className={`ph ${active ? "ph-on" : ""} ${interactive ? "ph-hover" : ""}`}
        tabIndex={interactive ? 0 : undefined}
        aria-label={interactive ? "Show the values in this part" : undefined}
        onMouseEnter={() => {
          if (!interactive) return;
          window.clearTimeout(timer.current);
          timer.current = window.setTimeout(show, 300);
        }}
        onMouseLeave={hide}
        onFocus={interactive ? show : undefined}
        onBlur={hide}
        onKeyDown={(e) => e.key === "Escape" && hide()}
        onClick={(e) => interactive && e.stopPropagation()}
      >
        &lt;*&gt;
      </span>
      {open && ref.current && ctx && patternId && (
        <Popover anchor={ref.current} onEnter={() => window.clearTimeout(timer.current)} onLeave={hide}>
          <PopBody
            data={data}
            color={ctx.colorOf(patternId)}
            onShowChart={() => {
              hide();
              ctx.onShowChart(patternId, index + 1);
            }}
          />
        </Popover>
      )}
    </>
  );
}

function Popover({ anchor, children, onEnter, onLeave }: { anchor: HTMLElement; children: React.ReactNode; onEnter: () => void; onLeave: () => void }) {
  const r = anchor.getBoundingClientRect();
  const below = window.innerHeight - r.bottom > 260;
  const style: React.CSSProperties = {
    position: "fixed",
    left: Math.min(Math.max(8, r.left - 20), window.innerWidth - 296),
    ...(below ? { top: r.bottom + 6 } : { bottom: window.innerHeight - r.top + 6 }),
    width: 280,
    zIndex: 80
  };
  return createPortal(
    <div className="dz valuepop-root" style={{ minHeight: 0 }}>
      <div className="tooltip valuepop" style={style} role="dialog" onMouseEnter={onEnter} onMouseLeave={onLeave}>
        {children}
      </div>
    </div>,
    document.body
  );
}

function PopBody({ data, color, onShowChart }: { data: FieldSummary | null | undefined; color: string; onShowChart: () => void }) {
  if (data === undefined) return <div className="sub">Loading…</div>;
  if (data === null) return <div className="sub">No values found for this part.</div>;
  if (data.kind === "distinct") {
    return (
      <>
        <div className="tt-h">Values here · {fmt(data.distinct)} distinct</div>
        <div className="sub">Every line has a different value (like an ID).</div>
        <div className="vp-ex mono">e.g. {data.top.slice(0, 2).map((v) => v.value).join(", ")}</div>
      </>
    );
  }
  const shown = data.top.reduce((s, v) => s + v.count, 0);
  const other = data.lines - shown;
  const maxC = Math.max(1, ...data.top.map((v) => v.count));
  const u = data.unit ?? "";
  return (
    <>
      <div className="tt-h">Values here · {fmt(data.distinct)} distinct</div>
      {data.overall && (
        <div className="sub vp-stats">
          Median {num(data.overall.median)}
          {u} · p95 {num(data.overall.p95)}
          {u} · Max {num(data.overall.max)}
          {u}
        </div>
      )}
      <div className="vp-rows">
        {data.top.map((v) => (
          <div key={v.value} className="vp-row">
            <span className="mono vp-val" title={v.value}>
              {v.value}
            </span>
            <i style={{ width: `${(v.count / maxC) * 80}px`, background: color }} />
            <b className="tnum">{fmt(v.count)}</b>
            <em className="tnum">{pct(v.count, data.lines)}</em>
          </div>
        ))}
        {other > 0 && (
          <div className="vp-row vp-other">
            <span>Other</span>
            <i />
            <b className="tnum">{fmt(other)}</b>
            <em className="tnum">{pct(other, data.lines)}</em>
          </div>
        )}
      </div>
      <button className="linkbtn vp-chart" onClick={onShowChart}>
        Show chart
      </button>
    </>
  );
}
