import { describe, it, expect } from 'vitest';
import {
  snapshotKnowledge, classifyError, decideViewState, allClearAllowed, degradedSourceSummary,
} from './knowledgeState.ts';
import { ApiError, SchemaCompatibilityError, ApiContractError } from './api.ts';

describe('snapshotKnowledge', () => {
  it('reports complete when completeness=complete and all sources available', () => {
    const k = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }, { status: 'available' }] });
    expect(k.level).toBe('complete');
    expect(k.incomplete).toBe(false);
  });

  it('is incomplete (unknown) with no meta', () => {
    const k = snapshotKnowledge(null);
    expect(k.level).toBe('unknown');
    expect(k.incomplete).toBe(true);
  });

  it('takes the strictest source signal over the declared completeness', () => {
    // Declared "complete" but a source is unavailable -> the strict signal wins.
    const k = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }, { status: 'unavailable' }] });
    expect(k.level).toBe('unavailable');
    expect(k.unavailableSources).toBe(1);
    expect(k.incomplete).toBe(true);
  });

  it('orders unavailable > stale > partial', () => {
    expect(snapshotKnowledge({ sources: [{ status: 'partial' }, { status: 'stale' }] }).level).toBe('stale');
    expect(snapshotKnowledge({ sources: [{ status: 'stale' }, { status: 'unavailable' }] }).level).toBe('unavailable');
    expect(snapshotKnowledge({ sources: [{ status: 'partial' }] }).level).toBe('partial');
  });

  it('reflects a partial completeness even when no single source is degraded', () => {
    const k = snapshotKnowledge({ completeness: 'partial', sources: [{ status: 'available' }] });
    expect(k.level).toBe('partial');
    expect(k.incomplete).toBe(true);
  });

  it('models a fully-understood empty snapshot as `empty`, NOT unknown/incomplete (requirement D)', () => {
    // Backend `empty` completeness: every source healthy, no record exists.
    const k = snapshotKnowledge({ completeness: 'empty', sources: [{ status: 'available' }, { status: 'available' }] });
    expect(k.level).toBe('empty');
    expect(k.incomplete).toBe(false); // complete knowledge; there is simply nothing
    expect(k.degradedSources).toBe(0);
  });

  it('distinguishes empty (no records, known) from unknown (no meta)', () => {
    expect(snapshotKnowledge({ completeness: 'empty', sources: [{ status: 'available' }] }).level).toBe('empty');
    expect(snapshotKnowledge(null).level).toBe('unknown');
  });

  it('a degraded source still wins over an `empty` completeness (cannot confirm empty)', () => {
    const k = snapshotKnowledge({ completeness: 'empty', sources: [{ status: 'unavailable' }] });
    expect(k.level).toBe('unavailable');
    expect(k.incomplete).toBe(true);
  });
});

/**
 * THE PREVIEW IS NOT THE POPULATION.
 *
 * `meta.sources` is capped at MaxMetaSources (50) and, past the cap, deliberately
 * keeps the LEAST healthy first. `meta.sourceCounts` is the backend's tally over
 * every source the snapshot holds. Counting the preview answers "how bad is the
 * worst 50" while looking like an answer to "how bad is the fleet", and the two
 * numbers then appear on the SAME page: the Data sources tally reads 60 unavailable
 * and the caveat above it reads 50.
 *
 * The counterexample only bites when a single health BUCKET is larger than the cap.
 * A fleet of 60 healthy sources and one outage survives the cut intact, which is why
 * the shape below is 60 unavailable and 1 available rather than the other way round.
 */
describe('snapshotKnowledge over the COMPLETE source population', () => {
  const CAP = 50;
  const preview = (status: string, n = CAP) => Array.from({ length: n }, () => ({ status }));

  // A. sourceCounts present, preview truncated -> the complete tally wins.
  it('counts the whole population, not the 50 sources the meta had room for', () => {
    const k = snapshotKnowledge({
      completeness: 'partial',
      sources: preview('unavailable'),
      sourcesTruncated: true,
      sourceCounts: { total: 61, available: 1, partial: 0, stale: 0, unavailable: 60 },
    });
    expect(k.unavailableSources).toBe(60);
    expect(k.countsBounded).toBe(false);
    expect(k.level).toBe('unavailable');
    // The sentence the banner prints is the one the tally beside it prints.
    expect(degradedSourceSummary(k)).toBe('60 data sources are unavailable');
  });

  it('cannot disagree with the tally when several buckets overflow the preview', () => {
    // 5 unavailable + 60 stale + 55 partial = 120 degraded of 121, and the preview can
    // only carry 50 of them -- all unavailable and stale, none partial. Reading the
    // preview would report "5 unavailable and 45 stale" and lose 55 partial sources.
    const counts = { total: 121, available: 1, partial: 55, stale: 60, unavailable: 5 };
    const k = snapshotKnowledge({
      completeness: 'partial', sources: preview('stale'), sourcesTruncated: true, sourceCounts: counts,
    });
    expect([k.unavailableSources, k.staleSources, k.degradedSources]).toEqual([5, 60, 55]);
    expect(k.level).toBe('unavailable');
    expect(degradedSourceSummary(k))
      .toBe('5 data sources are unavailable, 60 are stale and 55 are partial');
  });

  it('reads the strictest level off the tally even when the preview never saw it', () => {
    // The preview is 50 partial sources; the one unavailable source is past the cut in
    // this (deliberately adversarial) payload. The level is still the worst of the
    // population, because the population is what was counted.
    const k = snapshotKnowledge({
      completeness: 'complete',
      sources: preview('partial'),
      sourcesTruncated: true,
      sourceCounts: { total: 60, available: 0, partial: 59, stale: 0, unavailable: 1 },
    });
    expect(k.level).toBe('unavailable');
    expect(k.incomplete).toBe(true);
  });

  it('a healthy tally is complete knowledge, however many sources there are', () => {
    const k = snapshotKnowledge({
      completeness: 'complete', sources: preview('available'), sourcesTruncated: true,
      sourceCounts: { total: 61, available: 61, partial: 0, stale: 0, unavailable: 0 },
    });
    expect(k.level).toBe('complete');
    expect(k.incomplete).toBe(false);
    expect(degradedSourceSummary(k)).toBe('');
  });

  // B. No tally, nothing cut off: the preview IS the population, so counting it is
  // still correct. This is the compatibility path, and it must not get worse.
  it('falls back to the source list when the backend sent no tally', () => {
    const k = snapshotKnowledge({ completeness: 'partial', sources: [{ status: 'unavailable' }, { status: 'stale' }] });
    expect([k.unavailableSources, k.staleSources]).toEqual([1, 1]);
    expect(k.countsBounded).toBe(false);
    expect(degradedSourceSummary(k)).toBe('1 data source is unavailable and 1 is stale');
  });

  // C. No tally AND the list was cut: the consumer KNOWS it is holding a preview. It
  // may not print the preview's count as if it were the fleet's.
  it('never passes off a truncated preview as the whole fleet', () => {
    const k = snapshotKnowledge({
      completeness: 'partial', sources: preview('unavailable'), sourcesTruncated: true,
    });
    expect(k.unavailableSources).toBe(CAP);
    expect(k.countsBounded).toBe(true);
    expect(k.level).toBe('unavailable');
    // Boundedness is explicit in the wording, so the number is read as a floor.
    expect(degradedSourceSummary(k)).toBe('at least 50 data sources are unavailable');
  });
});

describe('classifyError', () => {
  it('classifies a 404 as not-found', () => {
    expect(classifyError(new ApiError(404, 'no such entity'))).toBe('not-found');
  });
  it('classifies schema/contract violations as schema-error', () => {
    expect(classifyError(new SchemaCompatibilityError('pacto.dev/other'))).toBe('schema-error');
    expect(classifyError(new ApiContractError('kind/payload mismatch'))).toBe('schema-error');
  });
  it('classifies any other transport failure as backend-error', () => {
    expect(classifyError(new ApiError(500, 'boom'))).toBe('backend-error');
    expect(classifyError(new Error('network down'))).toBe('backend-error');
  });
});

describe('decideViewState', () => {
  const complete = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }] });
  const empty = snapshotKnowledge({ completeness: 'empty', sources: [{ status: 'available' }] });
  const partial = snapshotKnowledge({ sources: [{ status: 'partial' }] });
  const stale = snapshotKnowledge({ sources: [{ status: 'stale' }] });
  const unavailable = snapshotKnowledge({ sources: [{ status: 'unavailable' }] });

  it('loading takes precedence when there is nothing to show yet', () => {
    expect(decideViewState({ loading: true, itemCount: 0 })).toEqual({ kind: 'loading' });
  });

  // Every product view is polled: App.loadGlobal() advances refreshTick on a timer and
  // each view re-runs its query. If an in-flight refresh outranks the data already on
  // screen, the whole page is replaced by a loading shell every few seconds -- the
  // content disappears, the document shortens and the reader's scroll position is
  // clamped toward the top. Data on hand outranks a request in flight.
  it('a refresh in flight over data already on hand stays ready, never loading', () => {
    const s = decideViewState({ loading: true, itemCount: 3, knowledge: complete });
    expect(s.kind).toBe('ready');
    if (s.kind === 'ready') expect(s.revalidating).toBe(true);
  });

  it('a FAILED refresh over data already on hand stays ready and reports the failure', () => {
    const err = new ApiError(0, 'unreachable');
    const s = decideViewState({ loading: false, itemCount: 3, error: err, knowledge: complete });
    expect(s.kind).toBe('ready');
    if (s.kind === 'ready') expect(s.refreshError).toBe(err);
  });

  it('a 404 on a refresh must not turn a page that HAS data into a not-found screen', () => {
    const s = decideViewState({ loading: false, itemCount: 1, error: new ApiError(404, 'x'), knowledge: complete });
    expect(s.kind).toBe('ready');
  });

  it('ready without a refresh in flight reports neither revalidating nor a refresh error', () => {
    const s = decideViewState({ loading: false, itemCount: 3, knowledge: complete });
    expect(s.kind).toBe('ready');
    if (s.kind === 'ready') {
      expect(s.revalidating).toBeFalsy();
      expect(s.refreshError).toBeFalsy();
    }
  });

  it('surfaces backend/schema/not-found errors distinctly', () => {
    expect(decideViewState({ loading: false, itemCount: 0, error: new ApiError(404, 'x') }).kind).toBe('not-found');
    expect(decideViewState({ loading: false, itemCount: 0, error: new SchemaCompatibilityError('v') }).kind).toBe('schema-error');
    expect(decideViewState({ loading: false, itemCount: 0, error: new ApiError(0, 'unreachable') }).kind).toBe('backend-error');
  });

  it('distinguishes a genuinely empty fleet from filtered-empty', () => {
    expect(decideViewState({ loading: false, itemCount: 0, knowledge: complete }).kind).toBe('empty-fleet');
    expect(decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: complete }).kind).toBe('filtered-empty');
  });

  it('treats an `empty`-completeness snapshot as a genuine empty fleet, not empty-unknown (requirement D)', () => {
    const s = decideViewState({ loading: false, itemCount: 0, knowledge: empty });
    expect(s.kind).toBe('empty-fleet');
  });

  it('filtered-empty carries the snapshot knowledge so incompleteness is not hidden (requirement D)', () => {
    // Under complete/empty knowledge the caveat is silent, but the knowledge is present.
    const okState = decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: complete });
    expect(okState.kind).toBe('filtered-empty');
    if (okState.kind === 'filtered-empty') expect(okState.knowledge.incomplete).toBe(false);

    // Under partial / stale / unavailable knowledge the filtered-empty state still
    // carries incomplete knowledge, so the view can show BOTH facts.
    for (const k of [partial, stale, unavailable]) {
      const s = decideViewState({ loading: false, itemCount: 0, filtered: true, knowledge: k });
      expect(s.kind).toBe('filtered-empty');
      if (s.kind === 'filtered-empty') expect(s.knowledge.incomplete).toBe(true);
    }
  });

  it('NEVER treats zero-items-under-incomplete-knowledge as an empty/clear fleet', () => {
    const s = decideViewState({ loading: false, itemCount: 0, knowledge: partial });
    expect(s.kind).toBe('empty-unknown');
    if (s.kind === 'empty-unknown') expect(s.knowledge.incomplete).toBe(true);
  });

  it('reports ready (carrying knowledge) when items are present, even if degraded', () => {
    const s = decideViewState({ loading: false, itemCount: 3, knowledge: partial });
    expect(s.kind).toBe('ready');
    if (s.kind === 'ready') expect(s.knowledge.level).toBe('partial');
  });
});

describe('allClearAllowed (the non-negotiable rule)', () => {
  const complete = snapshotKnowledge({ completeness: 'complete', sources: [{ status: 'available' }] });
  const partial = snapshotKnowledge({ sources: [{ status: 'partial' }] });
  const unknown = snapshotKnowledge(null);

  it('allows all-clear only under complete knowledge with zero attention', () => {
    expect(allClearAllowed(complete, 0)).toBe(true);
  });
  it('forbids all-clear when there IS attention', () => {
    expect(allClearAllowed(complete, 3)).toBe(false);
  });
  it('forbids all-clear under partial or unknown knowledge, even with zero attention', () => {
    expect(allClearAllowed(partial, 0)).toBe(false);
    expect(allClearAllowed(unknown, 0)).toBe(false);
  });
});

describe('degradedSourceSummary (naming WHAT is missing, not just that something is)', () => {
  const k = (sources: Array<{ status: string }>) => snapshotKnowledge({ completeness: 'partial', sources });

  // The caveat this feeds used to read "Source unavailable -- this page may be
  // incomplete" on a page whose own source was Available. Attributing the gap to a
  // COUNT of sources is what makes it unreadable as a claim about the one on screen.
  it('counts the degraded sources by state, worst first', () => {
    expect(degradedSourceSummary(k([
      { status: 'available' }, { status: 'unavailable' }, { status: 'stale' }, { status: 'partial' },
    ]))).toBe('1 data source is unavailable, 1 is stale and 1 is partial');
  });

  it('says the noun once and agrees the verb with the count', () => {
    expect(degradedSourceSummary(k([{ status: 'unavailable' }]))).toBe('1 data source is unavailable');
    expect(degradedSourceSummary(k([{ status: 'stale' }, { status: 'stale' }])))
      .toBe('2 data sources are stale');
    // The noun is not repeated in the later clauses -- and no serial comma.
    const two = degradedSourceSummary(k([{ status: 'unavailable' }, { status: 'partial' }, { status: 'partial' }]));
    expect(two).toBe('1 data source is unavailable and 2 are partial');
    expect(two.match(/data source/g)).toHaveLength(1);
    expect(two).not.toContain(', and');
  });

  it('is empty when nothing about the SOURCES is degraded', () => {
    // Completeness can be partial for reasons no source reported. The caller must then
    // fall back to its own wording rather than print an empty accusation.
    expect(degradedSourceSummary(k([{ status: 'available' }]))).toBe('');
    expect(degradedSourceSummary(snapshotKnowledge(null))).toBe('');
  });
});
