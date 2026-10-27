import { describe, it, expect } from 'vitest';
import { postureAnswer, joinAnd } from './answers.ts';

describe('postureAnswer', () => {
  it('names confirmed problems first', () => {
    expect(postureAnswer({ targets: 5, invalidRevisions: 1, compliance: { compliant: 2, nonCompliant: 3 } }))
      .toEqual({ tone: 'err', lead: 'Needs work.', rest: '3 running targets break their contract and 1 revision is invalid.' });
  });
  it('never says healthy when data is missing', () => {
    expect(postureAnswer({ targets: 2, compliance: { compliant: 2 } }, true).lead).toBe('Nothing broken found.');
    expect(postureAnswer({ targets: 2, compliance: { compliant: 2 } }, false).lead).toBe('Healthy.');
  });
  it('treats unknown compliance as not proven', () => {
    expect(postureAnswer({ targets: 2, compliance: { compliant: 1, unknown: 1 } }).rest).toBe('1 running target has unknown compliance.');
  });
  it('says nothing runs when nothing runs', () => {
    expect(postureAnswer({ targets: 0 }).lead).toBe('Nothing running yet.');
  });
  it('joins without an Oxford comma', () => {
    expect(joinAnd(['a', 'b', 'c'])).toBe('a, b and c');
  });
});

import { revisionAnswer } from './answers.ts';
describe('revisionAnswer', () => {
  it('leads with invalid', () => {
    expect(revisionAnswer({ valid: false, validation: { total: 2 } })).toEqual({ tone: 'err', lead: 'Invalid contract.', rest: '2 validation findings below.' });
  });
  it('separates valid from ready', () => {
    expect(revisionAnswer({ valid: true, readiness: { passing: false, score: 40, minScore: 60 } }).lead).toBe('Valid, not ready.');
    expect(revisionAnswer({ valid: true, readiness: { passing: true, score: 70, minScore: 60 } }).rest).toBe('Readiness 70, 60 required.');
    expect(revisionAnswer({ valid: true, readiness: null }).rest).toBe('No readiness gate declared.');
  });
});
