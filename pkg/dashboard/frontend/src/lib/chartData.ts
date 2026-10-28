import { ownerKey, UNOWNED_KEY } from './owners';

/**
 * Quadrant data: distance from a service's own readiness threshold (x) vs blast
 * radius (y).
 *
 * x is `score - minScore`, not the raw score, because the threshold is per service.
 * The chart's whole claim is "left of the line needs work", and against a fixed
 * midpoint that claim was false: a service scoring 60 against a gate of 80 sat on the
 * healthy side of a line drawn at 50 while failing its own gate, and a service scoring
 * 45 against a gate of 40 sat on the failing side while passing. Relative to its own
 * threshold, 0 is the gate for every point on the plot.
 *
 * A service with no declared threshold is measured against 0 — every score clears it,
 * which is exactly what "nobody set a bar" means.
 */
export interface QuadrantItem {
  name: string;
  x: number; // score - minScore: negative is below its own gate
  y: number; // blastRadius
  status: string;
}

export function quadrantData(
  services: Array<{
    name: string;
    contractStatus?: string;
    blastRadius?: number;
    readiness?: { score?: number; minScore?: number } | null;
  }>
): QuadrantItem[] {
  return services
    .filter((svc) => svc.readiness?.score != null)
    .map((svc) => ({
      name: svc.name,
      x: svc.readiness!.score! - (svc.readiness!.minScore ?? 0),
      y: svc.blastRadius ?? 0,
      status: svc.contractStatus || '',
    }));
}

/** Heatmap cell: owner × category with aggregated score. */
export interface HeatmapCell {
  owner: string;
  category: string;
  score: number; // 0–100, percent done
  n: number; // total checks in this cell
}

/** Heatmap aggregation: owners, categories (sorted), and cells. */
export interface HeatmapData {
  owners: string[];
  categories: string[];
  cells: HeatmapCell[];
}

export function heatmapData(
  services: Array<{
    owner?: unknown;
    readiness?: {
      checks?: Array<{ category?: string; status?: string }>;
    } | null;
  }>
): HeatmapData {
  // Aggregate by owner × category
  const cellMap = new Map<string, { done: number; total: number }>();
  const ownerSet = new Set<string>();
  const categorySet = new Set<string>();

  for (const svc of services) {
    // Canonical identity, so a team and a person of the same name are two rows.
    const owner = ownerKey(svc.owner) || UNOWNED_KEY;
    const checks = svc.readiness?.checks || [];

    if (checks.length === 0) continue;

    ownerSet.add(owner);

    for (const check of checks) {
      const cat = check.category || 'other';
      categorySet.add(cat);

      const key = `${owner}\0${cat}`;
      const cell = cellMap.get(key) ?? { done: 0, total: 0 };
      cell.total++;
      if (check.status === 'done') cell.done++;
      cellMap.set(key, cell);
    }
  }

  // Build cells with score
  const cells: HeatmapCell[] = [];
  for (const [key, { done, total }] of cellMap) {
    const [owner, category] = key.split('\0');
    cells.push({
      owner,
      category,
      score: total > 0 ? Math.round((100 * done) / total) : 0,
      n: total,
    });
  }

  return {
    owners: Array.from(ownerSet).sort(),
    categories: Array.from(categorySet).sort(),
    cells,
  };
}

/**
 * worstOwners keeps the limit teams with the lowest mean readiness, in that order,
 * and only their cells; a heatmap of every team grew 42px per team.
 */
export function worstOwners(data: HeatmapData, limit: number): HeatmapData {
  if (!data?.owners || data.owners.length <= limit) return data;
  const mean = new Map<string, { sum: number; n: number }>();
  for (const c of data.cells) {
    const m = mean.get(c.owner) ?? { sum: 0, n: 0 };
    m.sum += c.score; m.n++;
    mean.set(c.owner, m);
  }
  const avg = (o: string) => { const m = mean.get(o); return m && m.n ? m.sum / m.n : 0; };
  const owners = [...data.owners].sort((a, b) => avg(a) - avg(b) || a.localeCompare(b)).slice(0, limit);
  const keep = new Set(owners);
  return { ...data, owners, cells: data.cells.filter((c) => keep.has(c.owner)) };
}

/** Version timeline item sorted by creation date. */
export interface VersionTimelineItem {
  version: string;
  at: number; // epoch ms
  classification?: string;
  isCurrent?: boolean;
}

/** Past this many versions the timeline draws one marker per week (see binTimeline). */
const TIMELINE_MAX_MARKERS = 60;
const WEEK = 7 * 86400000;
const CLASS_RANK: Record<string, number> = { BREAKING: 3, POTENTIAL_BREAKING: 2, NON_BREAKING: 1 };

/**
 * binTimeline keeps up to max markers as they are; past that, one marker per week
 * stands for every version published that week, coloured by the worst change in it
 * and named by its newest version. Fifteen hundred markers were a solid bar of
 * overlapping hit targets.
 */
export function binTimeline(items: VersionTimelineItem[], max = TIMELINE_MAX_MARKERS): Array<VersionTimelineItem & { count?: number }> {
  if (items.length <= max) return items;
  const weeks = new Map<number, VersionTimelineItem & { count: number }>();
  for (const it of items) {
    const w = Math.floor(it.at / WEEK) * WEEK;
    const b = weeks.get(w);
    if (!b) { weeks.set(w, { ...it, at: w, count: 1 }); continue; }
    b.count++;
    b.version = it.version; // items are oldest first, so the last one is the newest
    b.isCurrent = b.isCurrent || it.isCurrent;
    if ((CLASS_RANK[it.classification || ''] ?? 0) > (CLASS_RANK[b.classification || ''] ?? 0)) b.classification = it.classification;
  }
  return [...weeks.values()];
}

export function versionTimelineData(
  versions: Array<{
    version?: string;
    createdAt?: string;
    classification?: string;
    isCurrent?: boolean;
  }>
): VersionTimelineItem[] {
  return versions
    .filter((v) => v.createdAt) // Drop undated
    .map((v) => {
      const ts = new Date(v.createdAt!).getTime();
      return {
        version: v.version || '',
        at: ts,
        classification: v.classification || '',
        isCurrent: v.isCurrent,
      };
    })
    .filter((v) => !isNaN(v.at)) // Drop invalid dates
    .sort((a, b) => a.at - b.at);
}
