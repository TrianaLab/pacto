import { ownerKey, UNOWNED_KEY } from './format';

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

/** Version timeline item sorted by creation date. */
export interface VersionTimelineItem {
  version: string;
  at: number; // epoch ms
  classification?: string;
  isCurrent?: boolean;
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
