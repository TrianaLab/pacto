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
  reasonLabel,
  reasonTooltip,
  reasonBadgeClass,
  isReasonActionable,
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

describe('reasonLabel', () => {
  it('returns External for non_oci_ref', () => expect(reasonLabel('non_oci_ref')).toBe('External'));
  it('returns Auth required for auth_failed', () => expect(reasonLabel('auth_failed')).toBe('Auth required'));
  it('returns No versions for no_semver_tags', () => expect(reasonLabel('no_semver_tags')).toBe('No versions'));
  it('returns Not found for not_found', () => expect(reasonLabel('not_found')).toBe('Not found'));
  it('returns Discovering… for discovering', () => expect(reasonLabel('discovering')).toBe('Discovering…'));
  it('returns External for undefined', () => expect(reasonLabel(undefined)).toBe('External'));
  it('returns External for empty string', () => expect(reasonLabel('')).toBe('External'));
  it('returns External for unknown reason', () => expect(reasonLabel('something_else')).toBe('External'));
});

describe('reasonTooltip', () => {
  it('returns non-OCI tooltip', () => expect(reasonTooltip('non_oci_ref')).toContain('Non-OCI'));
  it('returns auth tooltip', () => expect(reasonTooltip('auth_failed')).toContain('authentication'));
  it('returns no semver tooltip', () => expect(reasonTooltip('no_semver_tags')).toContain('semver'));
  it('returns not found tooltip', () => expect(reasonTooltip('not_found')).toContain('found'));
  it('returns discovering tooltip', () => expect(reasonTooltip('discovering')).toContain('discovery'));
  it('returns fallback for undefined', () => expect(reasonTooltip(undefined)).toBe('External dependency'));
  it('returns fallback for unknown reason', () => expect(reasonTooltip('xyz')).toBe('External dependency'));
});

describe('reasonBadgeClass', () => {
  it('returns badge-neutral for non_oci_ref', () => expect(reasonBadgeClass('non_oci_ref')).toBe('badge-neutral'));
  it('returns badge-err for auth_failed', () => expect(reasonBadgeClass('auth_failed')).toBe('badge-err'));
  it('returns badge-warn for no_semver_tags', () => expect(reasonBadgeClass('no_semver_tags')).toBe('badge-warn'));
  it('returns badge-warn for not_found', () => expect(reasonBadgeClass('not_found')).toBe('badge-warn'));
  it('returns badge-info for discovering', () => expect(reasonBadgeClass('discovering')).toBe('badge-info'));
  it('returns badge-neutral for undefined', () => expect(reasonBadgeClass(undefined)).toBe('badge-neutral'));
  it('returns badge-neutral for unknown reason', () => expect(reasonBadgeClass('other')).toBe('badge-neutral'));
});

describe('isReasonActionable', () => {
  it('returns true for auth_failed', () => expect(isReasonActionable('auth_failed')).toBe(true));
  it('returns false for non_oci_ref', () => expect(isReasonActionable('non_oci_ref')).toBe(false));
  it('returns true for no_semver_tags', () => expect(isReasonActionable('no_semver_tags')).toBe(true));
  it('returns true for not_found', () => expect(isReasonActionable('not_found')).toBe(true));
  it('returns false for discovering', () => expect(isReasonActionable('discovering')).toBe(false));
  it('returns false for undefined', () => expect(isReasonActionable(undefined)).toBe(false));
});
