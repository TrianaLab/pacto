// Words for range coverage and announced breaks, as pacto impact prints them.

type Coverage = { edges: number; declared: number; accepted: number; unknown?: { from: string; to: string; proposed?: string }[] | null; unknownTotal?: number };
type BreakState = { state: string; by?: string; record?: string; expires?: string; route?: string; reason?: string };

/** The Ranges: line: `Ranges: 41 declared and 6 accepted of 58 dependency edges (81%)`. */
export function rangesLine(r: Coverage): string {
  if (!r.edges) return 'Ranges: no dependency edges';
  const pct = Math.floor(((r.declared + r.accepted) * 100) / r.edges);
  return `Ranges: ${r.declared} declared and ${r.accepted} accepted of ${r.edges} dependency edges (${pct}%)`;
}

/** The unknown ranges, one per line, then how many more there are. */
export function unknownRanges(r: Coverage): string[] {
  const lines = (r.unknown ?? []).map((u) => `unknown: ${u.from} -> ${u.to}${u.proposed ? ` (proposed ${u.proposed})` : ''}`);
  const more = (r.unknownTotal ?? 0) - lines.length;
  return more > 0 ? [...lines, `and ${more} more`] : lines;
}

/** The tip of a consumer's break mark: its state, who signed, the record, the expiry and the route. */
export function breakText(b: BreakState): string {
  const words: Record<string, string> = {
    accepted: 'Break accepted: it never blocks.',
    announced: 'Break announced: nothing accepts it yet.',
    expired: 'Break expired or records no expiry: it keeps its verdict.',
  };
  return [words[b.state] ?? `Break ${b.state}.`, b.by && `Signed by ${b.by}.`, b.record && `Record: ${b.record}.`,
    b.expires ? `Expires ${b.expires}.` : 'Expiry unknown.', b.route && `Waiting for ${b.route}.`, b.reason].filter(Boolean).join(' ');
}
