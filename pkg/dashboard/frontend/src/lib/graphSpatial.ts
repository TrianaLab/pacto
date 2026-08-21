/**
 * Browser-local persistence for Operational Graph SPATIAL state.
 *
 * Spatial state is presentation geometry and nothing else: where each node sits, and
 * how the viewport is framed. It carries no semantics, so a stale or foreign entry can
 * only misplace a node -- never misreport a status, a compliance verdict or an identity.
 * That is why it is safe to keep in the browser at all, and why it is NEVER part of the
 * shareable URL: a URL is a claim about WHAT you are looking at, not about where you
 * happened to drag a box.
 *
 * It is keyed by the canonical graph QUERY identity, so state can never leak between two
 * different questions (a different focus, perspective, knowledge view, direction or
 * depth is a different graph and gets its own arrangement).
 *
 * sessionStorage, not localStorage: an arrangement is a working-session artefact. It
 * should survive a reload and a back/forward, and it should NOT still be waiting a week
 * later for a topology that has since changed.
 */

/** The stored shape. `v` is checked on read: a future version reads as absent rather
 *  than as a half-understood record. */
export const SPATIAL_VERSION = 1;

export interface Point { x: number; y: number }
export interface SpatialRecord {
  v: number;
  positions: Record<string, Point>;
  pan: Point;
  zoom: number;
}

/** The identity fields of a graph query. Structurally satisfied by GraphState. */
export interface GraphQuery {
  kind: string;
  key: string;
  perspective: string;
  views: readonly string[];
  direction: string;
  depth: number;
}

const PREFIX = 'pacto.graph.spatial.v1:';
const INDEX_KEY = 'pacto.graph.spatial.v1.index';
// Bounded by construction, like every other product surface: at most this many node
// positions per entry, and at most this many entries kept at all (least-recently-saved
// evicted first). A neighborhood is already bounded server-side, so these are a backstop
// against an unbounded browser store, not a working limit.
const MAX_NODES = 400;
const MAX_ENTRIES = 8;

/** graphQueryKey is the canonical identity of a graph QUERY: same question, same key.
 *  Views are sorted so that selecting them in a different order is still the same
 *  question. The requested depth is part of the identity -- a deeper query is a
 *  different graph, and gets its own arrangement rather than inheriting one. */
export function graphQueryKey(q: GraphQuery): string {
  return [
    q.kind || '',
    q.key || '',
    q.perspective || '',
    [...(q.views || [])].sort().join('+'),
    q.direction || '',
    String(q.depth ?? ''),
  ].join('|');
}

// Accessing sessionStorage THROWS (not returns null) in a sandboxed iframe or with
// site data blocked, so every access is guarded. Persistence is a convenience: when it
// is unavailable the graph simply lays out fresh every time.
function store(): Storage | null {
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch {
    return null;
  }
}

function finitePoint(p: unknown): Point | null {
  if (!p || typeof p !== 'object') return null;
  const { x, y } = p as Point;
  return Number.isFinite(x) && Number.isFinite(y) ? { x, y } : null;
}

/** parseRecord validates a stored entry hard. Anything it cannot vouch for reads as
 *  absent, so a corrupt or hand-edited entry can never leave the graph unusable -- the
 *  caller just lays out fresh. */
function parseRecord(raw: string | null): SpatialRecord | null {
  if (!raw) return null;
  let parsed: unknown;
  try {
    parsed = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!parsed || typeof parsed !== 'object') return null;
  const r = parsed as Partial<SpatialRecord>;
  if (r.v !== SPATIAL_VERSION) return null;
  const pan = finitePoint(r.pan);
  if (!pan) return null;
  if (typeof r.zoom !== 'number' || !Number.isFinite(r.zoom) || r.zoom <= 0) return null;
  if (!r.positions || typeof r.positions !== 'object') return null;
  const positions: Record<string, Point> = {};
  let n = 0;
  for (const [id, p] of Object.entries(r.positions)) {
    const pt = finitePoint(p);
    if (!pt) continue;
    positions[id] = pt;
    if (++n >= MAX_NODES) break;
  }
  if (n === 0) return null; // a viewport with nothing to frame is not worth restoring
  return { v: SPATIAL_VERSION, positions, pan, zoom: r.zoom };
}

function readIndex(s: Storage): string[] {
  try {
    const v = JSON.parse(s.getItem(INDEX_KEY) || '[]');
    return Array.isArray(v) ? v.filter((k) => typeof k === 'string') : [];
  } catch {
    return [];
  }
}

function writeIndex(s: Storage, keys: string[]): void {
  try {
    s.setItem(INDEX_KEY, JSON.stringify(keys));
  } catch {
    /* nothing to do: the entries themselves are still self-describing */
  }
}

/** loadSpatial returns the saved arrangement for a query, or null. */
export function loadSpatial(queryKey: string): SpatialRecord | null {
  const s = store();
  if (!s) return null;
  try {
    return parseRecord(s.getItem(PREFIX + queryKey));
  } catch {
    return null;
  }
}

/** saveSpatial persists the arrangement for a query, evicting the oldest entries beyond
 *  the cap. A write failure (quota, private mode) is not an error the user needs to see:
 *  the arrangement simply will not survive the reload. */
export function saveSpatial(queryKey: string, state: { positions: Record<string, Point>; pan: Point; zoom: number }): void {
  const s = store();
  if (!s) return;
  const pan = finitePoint(state?.pan);
  if (!pan || !Number.isFinite(state?.zoom) || state.zoom <= 0) return;
  const positions: Record<string, Point> = {};
  // Sorted so that a graph over the cap keeps a STABLE subset across saves rather than
  // a different arbitrary slice each time.
  for (const id of Object.keys(state.positions || {}).sort().slice(0, MAX_NODES)) {
    const pt = finitePoint(state.positions[id]);
    if (pt) positions[id] = { x: Math.round(pt.x), y: Math.round(pt.y) };
  }
  if (!Object.keys(positions).length) return;
  const rec: SpatialRecord = { v: SPATIAL_VERSION, positions, pan: { x: Math.round(pan.x), y: Math.round(pan.y) }, zoom: state.zoom };
  const keys = readIndex(s).filter((k) => k !== queryKey);
  keys.push(queryKey);
  while (keys.length > MAX_ENTRIES) {
    const old = keys.shift();
    try {
      if (old) s.removeItem(PREFIX + old);
    } catch { /* already gone */ }
  }
  try {
    s.setItem(PREFIX + queryKey, JSON.stringify(rec));
    writeIndex(s, keys);
  } catch {
    /* quota or private mode: drop it silently */
  }
}

/** clearSpatial forgets the arrangement for one query ("Reset layout"). */
export function clearSpatial(queryKey: string): void {
  const s = store();
  if (!s) return;
  try {
    s.removeItem(PREFIX + queryKey);
    writeIndex(s, readIndex(s).filter((k) => k !== queryKey));
  } catch {
    /* nothing persisted, nothing to forget */
  }
}
