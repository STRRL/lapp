import React, { useEffect, useState } from "react";
import { X } from "lucide-react";

type Preview = {
  model: string;
  keySet: boolean;
  patterns: { template: string; samples: string[] }[];
  sampleLines: number;
  tokens: number;
  tokensMasked: number;
  mask: boolean;
  requests: number;
};

// Bits worth a second look before they leave the machine.
// Keep in sync with pkg/semantic/mask.go, which masks the same bits server-side.
const SENSITIVE: { kind: string; placeholder: string; re: RegExp }[] = [
  { kind: "Looks like an email", placeholder: "<email>", re: /[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}/g },
  { kind: "Looks like an IP address", placeholder: "<ip>", re: /\b(?:\d{1,3}\.){3}\d{1,3}\b|\b(?:[0-9a-f]{1,4}:){3,7}[0-9a-f]{1,4}\b/gi },
  { kind: "Looks like a token or key", placeholder: "<token>", re: /\b(?:eyJ[\w-]{10,}\.[\w-]{10,}\.[\w-]{5,}|(?:sk|pk|ghp|gho|xox[abp])[-_][A-Za-z0-9_-]{12,}|[A-Za-z0-9+/_-]{32,}={0,2})\b/g }
];

type Hit = { start: number; end: number; kind: string; placeholder: string };

function findHits(line: string): Hit[] {
  const hits: Hit[] = [];
  for (const { kind, placeholder, re } of SENSITIVE) {
    for (const m of line.matchAll(re)) {
      const start = m.index ?? 0;
      const end = start + m[0].length;
      if (!hits.some((h) => start < h.end && end > h.start)) hits.push({ start, end, kind, placeholder });
    }
  }
  return hits.sort((a, b) => a.start - b.start);
}

// With mask on, shows the placeholder that is actually sent; the original
// is only in the tooltip.
function Highlighted({ line, mask }: { line: string; mask: boolean }) {
  const hits = findHits(line);
  const out: React.ReactNode[] = [];
  let last = 0;
  hits.forEach((h, i) => {
    out.push(line.slice(last, h.start));
    const original = line.slice(h.start, h.end);
    out.push(
      mask ? (
        <span key={i} className="ph masked" title={`${h.kind}: ${original} (not sent)`}>
          {h.placeholder}
        </span>
      ) : (
        <mark key={i} className="sens" title={h.kind}>
          {original}
        </mark>
      )
    );
    last = h.end;
  });
  out.push(line.slice(last));
  return <>{out}</>;
}

export function PreviewDialog({
  wsId,
  service,
  onClose,
  onSent
}: {
  wsId: string;
  service: string;
  onClose: () => void;
  onSent: () => void;
}) {
  const [data, setData] = useState<Preview | null>(null);
  const [error, setError] = useState("");
  const [remember, setRemember] = useState(false);
  const [mask, setMask] = useState(true);
  const [sending, setSending] = useState(false);
  useEffect(() => {
    fetch(`/api/demo/llm-preview?ws=${encodeURIComponent(wsId)}`)
      .then(async (r) => (r.ok ? r.json() : Promise.reject(new Error((await r.json()).error))))
      .then((d: Preview) => {
        setData(d);
        setMask(d.mask);
      })
      .catch((e) => setError(String(e.message ?? e)));
  }, [wsId]);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  const flagged = data ? data.patterns.flatMap((p) => [p.template, ...p.samples]).reduce((n, l) => n + findHits(l).length, 0) : 0;

  const send = async () => {
    setSending(true);
    setError("");
    const res = await fetch(`/api/demo/name?ws=${encodeURIComponent(wsId)}&mask=${mask ? 1 : 0}${remember ? "&remember=1" : ""}`, { method: "POST" });
    if (!res.ok) {
      setError((await res.json()).error ?? res.statusText);
      setSending(false);
      return;
    }
    onSent();
  };

  return (
    <div className="modal-scrim" onClick={onClose}>
      <div className="modal" role="dialog" aria-label="What lapp will send to OpenRouter" onClick={(e) => e.stopPropagation()}>
        <header>
          <b className="dtitle">What lapp will send to OpenRouter</b>
          <button className="btn ghost sm" aria-label="Close" onClick={onClose}>
            <X className="ico" />
          </button>
        </header>
        <p className="lead-plain">To name patterns, lapp sends these sample lines. Nothing else is sent.</p>
        {data && (
          <div className="modal-stats tnum">
            {data.patterns.length} patterns · {data.sampleLines} sample lines · about {(mask ? data.tokensMasked : data.tokens).toLocaleString("en-US")} tokens ·
            model {data.model}
          </div>
        )}
        {flagged > 0 && (
          <div className="modal-warn">
            <span>
              {mask
                ? `${flagged} ${flagged === 1 ? "bit" : "bits"} will be masked.`
                : `${flagged} highlighted ${flagged === 1 ? "bit looks" : "bits look"} like emails, IP addresses or tokens. Check them before sending.`}
            </span>
            <label className="mask-toggle" title="Replaces highlighted bits with <ip>, <email> or <token>. Naming only needs the sentence shape.">
              <input type="checkbox" checked={mask} onChange={(e) => setMask(e.target.checked)} />
              Mask before sending
            </label>
          </div>
        )}
        <div className="modal-body">
          {!data && !error && <p className="sub">Loading…</p>}
          {data?.patterns.map((p, i) => (
            <section key={i} className="pv-block">
              <div className="pv-tpl mono">
                <Highlighted line={p.template} mask={mask} />
              </div>
              <pre className="pv-lines mono">
                {p.samples.map((l, j) => (
                  <div key={j}>
                    <Highlighted line={l} mask={mask} />
                  </div>
                ))}
              </pre>
            </section>
          ))}
        </div>
        {error && <p className="err">{error}</p>}
        <footer>
          <label className="chk">
            <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
            <span className="cb">✓</span>
            Don't ask again for {service}
          </label>
          <div style={{ flex: 1 }} />
          {data && !data.keySet && <span className="sub">Set OPENROUTER_API_KEY and restart lapp to name patterns.</span>}
          <button className="btn" onClick={onClose}>
            Not now
          </button>
          <button
            className="btn primary"
            disabled={!data || sending || !data.keySet}
            title={data && !data.keySet ? "Set OPENROUTER_API_KEY and restart lapp to name patterns." : undefined}
            onClick={send}
          >
            Send and name patterns
          </button>
        </footer>
      </div>
    </div>
  );
}
