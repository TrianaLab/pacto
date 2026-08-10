import { describe, it, expect } from 'vitest';
import {
  snapshotKnowledge, classifyError, decideViewState, allClearAllowed,
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
