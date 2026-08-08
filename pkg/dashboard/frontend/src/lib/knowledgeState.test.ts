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
  const partial = snapshotKnowledge({ sources: [{ status: 'partial' }] });

  it('loading takes precedence', () => {
    expect(decideViewState({ loading: true, itemCount: 0 })).toEqual({ kind: 'loading' });
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
