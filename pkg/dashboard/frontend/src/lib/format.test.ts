import { describe, it, expect } from 'vitest';
import {
  statusClass,
  complianceClass,
  complianceStatusClass,
  methodClass,
  classificationClass,
  changeTypeClass,
  sourceTooltip,
  formatDiffValue,
} from './format.ts';

describe('statusClass', () => {
  it('maps Compliant to ok', () => expect(statusClass('Compliant')).toBe('ok'));
  it('maps Warning to warn', () => expect(statusClass('Warning')).toBe('warn'));
  it('maps NonCompliant to err', () => expect(statusClass('NonCompliant')).toBe('err'));
  it('maps Unknown to neutral', () => expect(statusClass('Unknown')).toBe('neutral'));
  it('maps Reference to neutral', () => expect(statusClass('Reference')).toBe('neutral'));
  it('maps undefined to neutral', () => expect(statusClass(undefined)).toBe('neutral'));
});

describe('complianceClass', () => {
  it('returns score-ok for >= 80', () => expect(complianceClass(80)).toBe('score-ok'));
  it('returns score-ok for 100', () => expect(complianceClass(100)).toBe('score-ok'));
  it('returns score-warn for >= 50 and < 80', () => expect(complianceClass(50)).toBe('score-warn'));
  it('returns score-warn for 79', () => expect(complianceClass(79)).toBe('score-warn'));
  it('returns score-err for < 50', () => expect(complianceClass(49)).toBe('score-err'));
  it('returns score-err for 0', () => expect(complianceClass(0)).toBe('score-err'));
});

describe('complianceStatusClass', () => {
  it('maps OK to score-ok', () => expect(complianceStatusClass('OK')).toBe('score-ok'));
  it('maps WARNING to score-warn', () => expect(complianceStatusClass('WARNING')).toBe('score-warn'));
  it('maps ERROR to score-err', () => expect(complianceStatusClass('ERROR')).toBe('score-err'));
  it('returns empty string for unknown', () => expect(complianceStatusClass('other')).toBe(''));
});

describe('methodClass', () => {
  it('maps GET to badge-ok', () => expect(methodClass('GET')).toBe('badge-ok'));
  it('maps POST to badge-info', () => expect(methodClass('POST')).toBe('badge-info'));
  it('maps PUT to badge-warn', () => expect(methodClass('PUT')).toBe('badge-warn'));
  it('maps PATCH to badge-warn', () => expect(methodClass('PATCH')).toBe('badge-warn'));
  it('maps DELETE to badge-err', () => expect(methodClass('DELETE')).toBe('badge-err'));
  it('maps unknown to badge-neutral', () => expect(methodClass('OPTIONS')).toBe('badge-neutral'));
  it('is case-insensitive', () => expect(methodClass('get')).toBe('badge-ok'));
  it('handles null/undefined', () => expect(methodClass(null)).toBe('badge-neutral'));
});

describe('classificationClass', () => {
  it('maps BREAKING to badge-err', () => expect(classificationClass('BREAKING')).toBe('badge-err'));
  it('maps POTENTIAL_BREAKING to badge-warn', () => expect(classificationClass('POTENTIAL_BREAKING')).toBe('badge-warn'));
  it('maps NON_BREAKING to badge-ok', () => expect(classificationClass('NON_BREAKING')).toBe('badge-ok'));
  it('maps unknown to badge-neutral', () => expect(classificationClass('other')).toBe('badge-neutral'));
});

describe('changeTypeClass', () => {
  it('maps added to diff-added', () => expect(changeTypeClass('added')).toBe('diff-added'));
  it('maps removed to diff-removed', () => expect(changeTypeClass('removed')).toBe('diff-removed'));
  it('maps modified to diff-modified', () => expect(changeTypeClass('modified')).toBe('diff-modified'));
  it('returns empty for unknown', () => expect(changeTypeClass('other')).toBe(''));
});

describe('sourceTooltip', () => {
  it('returns description for known sources', () => {
    expect(sourceTooltip('k8s')).toContain('Kubernetes');
    expect(sourceTooltip('oci')).toContain('OCI');
    expect(sourceTooltip('local')).toContain('Local');
  });

  it('returns the input for unknown sources', () => {
    expect(sourceTooltip('custom')).toBe('custom');
    expect(sourceTooltip('cache')).toBe('cache');
  });
});

describe('formatDiffValue', () => {
  it('returns dash for null', () => expect(formatDiffValue(null)).toBe('—'));
  it('returns dash for undefined', () => expect(formatDiffValue(undefined)).toBe('—'));
  it('returns string for string input', () => expect(formatDiffValue('hello')).toBe('hello'));
  it('returns string for number input', () => expect(formatDiffValue(42)).toBe('42'));
  it('returns JSON for objects', () => {
    expect(formatDiffValue({ a: 1 })).toBe('{\n  "a": 1\n}');
  });
  it('returns JSON for arrays', () => {
    expect(formatDiffValue([1, 2])).toBe('[\n  1,\n  2\n]');
  });
  it('returns "false" for boolean false', () => expect(formatDiffValue(false)).toBe('false'));
  it('returns "0" for zero', () => expect(formatDiffValue(0)).toBe('0'));
});
