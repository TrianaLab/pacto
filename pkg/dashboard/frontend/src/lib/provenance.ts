/**
 * Provenance (every fleet figure): where a figure's numbers came from, in words. Its
 * own module, so the eager list views that state it do not load the figure models.
 */
import { countOf, formatCount } from './format.ts';

export interface SourceTally { total?: number; available?: number; partial?: number; stale?: number; unavailable?: number }

export interface MetaLike { asOf?: string; completeness?: string; sourceCounts?: SourceTally | null; limitations?: { code?: string }[] | null }

/**
 * provenance is where a fleet figure's numbers came from, in words: the sources
 * behind the snapshot and how complete they are ("Partial: 2 of 50 sources
 * unavailable, 1 stale"). It is the provenance strip under a figure and the
 * Evidence fact of an aside; partial is never shown as complete.
 */
export function provenance(meta: MetaLike | null | undefined) {
  const c = meta?.sourceCounts ?? {};
  const n = c.total ?? 0;
  const bad = (['unavailable', 'stale', 'partial'] as const).filter((st) => (c[st] ?? 0) > 0);
  const partial = bad.length > 0 || meta?.completeness === 'partial';
  let text: string;
  if (bad.length) {
    const [first, ...rest] = bad;
    text = `Partial: ${formatCount(c[first] ?? 0)} of ${countOf(n, 'source')} ${first}${rest.map((st) => `, ${formatCount(c[st] ?? 0)} ${st}`).join('')}`;
  } else if (partial) {
    text = 'Partial: some knowledge is missing';
  } else {
    text = n === 1 ? 'Complete: its one source is current' : `Complete: all ${formatCount(n)} sources are current`;
  }
  // Query.Restrict marks the answer with this limitation when the caller's access hides
  // services. It carries no count: hidden services are never counted into a total.
  // ponytail: read off the bounded limitations list, so it can miss when more than
  // MaxMetaLimitations codes sort before it; a meta flag would make it exact.
  const hidden = !!meta?.limitations?.some((l) => l.code === 'HIDDEN_BY_PERMISSIONS');
  return { asOf: meta?.asOf ?? '', sources: n, partial, text, hidden };
}
