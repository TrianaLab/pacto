import { describe, it, expect } from 'vitest';
import {
  statusClass,
  complianceClass,
  complianceStatusClass,
  methodClass,
  referencedDocPaths,
  classificationClass,
  changeTypeClass,
  sourceTooltip,
  formatDiffValue,
  reasonLabel,
  reasonTooltip,
  reasonBadgeClass,
  isReasonActionable,
  ownerDisplay,
  ownerKey,
  ownerTeam,
  ownerMatchesFilter,
  ownerIsStructured,
  aggregateByOwner,
  compareScoresUnassessedLast,
  extractOwnerDetail,
  computeTooltipPosition,
  versionPolicyLabel,
  versionPolicyClass,
  countHighImpact,
  filterServices,
  paginate,
  readinessBucket,
  readinessBucketLabel,
  readinessBucketClass,
  readinessGateClass,
  readinessGateTip,
  summarizeReadiness,
  isUrlEvidence,
  readinessCheckTypes,
  checkStatusLabel,
  checkStatusClass,
  assessmentCountdownLabel,
  summarizeFleet,
  summarize,
  shortDigest,
  driftBadgeClass,
  driftBadgeLabel,
} from './format.ts';

describe('statusClass', () => {
  it('maps Compliant to ok', () => expect(statusClass('Compliant')).toBe('ok'));
  it('maps Warning to warn', () => expect(statusClass('Warning')).toBe('warn'));
  it('maps NonCompliant to err', () => expect(statusClass('NonCompliant')).toBe('err'));
  it('maps Unknown to neutral', () => expect(statusClass('Unknown')).toBe('neutral'));
  it('maps Reference to reference', () => expect(statusClass('Reference')).toBe('reference'));
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

describe('referencedDocPaths', () => {
  it('returns [] for null/undefined or no checks', () => {
    expect(referencedDocPaths(null)).toEqual([]);
    expect(referencedDocPaths(undefined)).toEqual([]);
    expect(referencedDocPaths({})).toEqual([]);
  });
  it('returns only paths of checks with docPath', () => {
    const readiness = {
      checks: [
        { docPath: 'docs/runbook.md' },
        { docPath: '' },
        {},
        { docPath: 'docs/overview.md' },
      ],
    };
    expect(referencedDocPaths(readiness)).toEqual(['docs/runbook.md', 'docs/overview.md']);
  });
});

describe('checkStatusLabel', () => {
  it('maps each declared status to a human label', () => {
    expect(checkStatusLabel('done')).toBe('Done');
    expect(checkStatusLabel('partial')).toBe('Partial');
    expect(checkStatusLabel('not-done')).toBe('Not done');
    expect(checkStatusLabel('deferred')).toBe('Deferred');
  });
  it('falls back to the raw value for unknown statuses', () => expect(checkStatusLabel('weird')).toBe('weird'));
  it('returns a dash for undefined', () => expect(checkStatusLabel(undefined)).toBe('—'));
});

describe('checkStatusClass', () => {
  it('reuses the shared status palette', () => {
    expect(checkStatusClass('done')).toBe('badge-ok');
    expect(checkStatusClass('partial')).toBe('badge-warn');
    expect(checkStatusClass('not-done')).toBe('badge-err');
    expect(checkStatusClass('deferred')).toBe('badge-neutral');
  });
  it('defaults to neutral for unknown/undefined', () => {
    expect(checkStatusClass('weird')).toBe('badge-neutral');
    expect(checkStatusClass(undefined)).toBe('badge-neutral');
  });
});

describe('assessmentCountdownLabel', () => {
  it('reports Expired when the assessment is expired', () => {
    expect(assessmentCountdownLabel(true, 5)).toBe('Expired');
    expect(assessmentCountdownLabel(true, null)).toBe('Expired');
  });
  it('reports Expired when days are negative', () => expect(assessmentCountdownLabel(false, -1)).toBe('Expired'));
  it('returns empty when no expiry is declared', () => expect(assessmentCountdownLabel(false, null)).toBe(''));
  it('reports today for 0 days', () => expect(assessmentCountdownLabel(false, 0)).toBe('expires today'));
  it('reports singular for 1 day', () => expect(assessmentCountdownLabel(false, 1)).toBe('expires in 1 day'));
  it('reports plural for many days', () => expect(assessmentCountdownLabel(false, 30)).toBe('expires in 30 days'));
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
    expect(sourceTooltip('cache')).toContain('Cache');
  });

  it('returns the input for unknown sources', () => {
    expect(sourceTooltip('custom')).toBe('custom');
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
  it('returns Unreachable for pull_failed', () => expect(reasonLabel('pull_failed')).toBe('Unreachable'));
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
  it('returns pull failed tooltip', () => expect(reasonTooltip('pull_failed')).toContain('unreachable'));
  it('returns discovering tooltip', () => expect(reasonTooltip('discovering')).toContain('discovery'));
  it('returns fallback for undefined', () => expect(reasonTooltip(undefined)).toBe('External dependency'));
  it('returns fallback for unknown reason', () => expect(reasonTooltip('xyz')).toBe('External dependency'));
});

describe('reasonBadgeClass', () => {
  it('returns badge-neutral for non_oci_ref', () => expect(reasonBadgeClass('non_oci_ref')).toBe('badge-neutral'));
  it('returns badge-err for auth_failed', () => expect(reasonBadgeClass('auth_failed')).toBe('badge-err'));
  it('returns badge-warn for no_semver_tags', () => expect(reasonBadgeClass('no_semver_tags')).toBe('badge-warn'));
  it('returns badge-warn for not_found', () => expect(reasonBadgeClass('not_found')).toBe('badge-warn'));
  it('returns badge-err for pull_failed', () => expect(reasonBadgeClass('pull_failed')).toBe('badge-err'));
  it('returns badge-info for discovering', () => expect(reasonBadgeClass('discovering')).toBe('badge-info'));
  it('returns badge-neutral for undefined', () => expect(reasonBadgeClass(undefined)).toBe('badge-neutral'));
  it('returns badge-neutral for unknown reason', () => expect(reasonBadgeClass('other')).toBe('badge-neutral'));
});

describe('isReasonActionable', () => {
  it('returns true for auth_failed', () => expect(isReasonActionable('auth_failed')).toBe(true));
  it('returns false for non_oci_ref', () => expect(isReasonActionable('non_oci_ref')).toBe(false));
  it('returns true for no_semver_tags', () => expect(isReasonActionable('no_semver_tags')).toBe(true));
  it('returns true for not_found', () => expect(isReasonActionable('not_found')).toBe(true));
  it('returns true for pull_failed', () => expect(isReasonActionable('pull_failed')).toBe(true));
  it('returns false for discovering', () => expect(isReasonActionable('discovering')).toBe(false));
  it('returns false for undefined', () => expect(isReasonActionable(undefined)).toBe(false));
});

describe('ownerDisplay', () => {
  it('returns empty for null', () => expect(ownerDisplay(null)).toBe(''));
  it('returns empty for undefined', () => expect(ownerDisplay(undefined)).toBe(''));
  it('returns empty for string (object-only now)', () => expect(ownerDisplay('team/payments')).toBe(''));
  it('returns team from structured', () => expect(ownerDisplay({ team: 'foundations' })).toBe('foundations'));
  it('returns dri when no team', () => expect(ownerDisplay({ dri: 'alice' })).toBe('alice'));
  it('returns empty for empty object', () => expect(ownerDisplay({})).toBe(''));
  it('prefers team over dri', () => expect(ownerDisplay({ team: 't', dri: 'd' })).toBe('t'));
});

describe('ownerKey', () => {
  it('is the same function as ownerDisplay', () => expect(ownerKey).toBe(ownerDisplay));
});

describe('ownerTeam', () => {
  it('returns empty for string (object-only now)', () => expect(ownerTeam('team/x')).toBe(''));
  it('returns team from structured', () => expect(ownerTeam({ team: 'a' })).toBe('a'));
  it('returns empty for null', () => expect(ownerTeam(null)).toBe(''));
});

describe('ownerMatchesFilter', () => {
  it('returns false for string (object-only now)', () => expect(ownerMatchesFilter('team/payments', 'pay')).toBe(false));
  it('matches structured team', () => expect(ownerMatchesFilter({ team: 'foundations' }, 'found')).toBe(true));
  it('matches structured dri', () => expect(ownerMatchesFilter({ dri: 'alice' }, 'ali')).toBe(true));
  it('matches structured contacts', () => {
    expect(ownerMatchesFilter({ contacts: [{ value: 'alice@acme.com' }] }, 'acme')).toBe(true);
  });
  it('case-insensitive', () => expect(ownerMatchesFilter({ team: 'TEAM' }, 'team')).toBe(true));
  it('returns false for null', () => expect(ownerMatchesFilter(null, 'x')).toBe(false));
});

describe('ownerIsStructured', () => {
  it('returns false for null', () => expect(ownerIsStructured(null)).toBe(false));
  it('returns false for string', () => expect(ownerIsStructured('str')).toBe(false));
  it('returns true for object', () => expect(ownerIsStructured({ team: 'x' })).toBe(true));
  it('returns true for empty object', () => expect(ownerIsStructured({})).toBe(true));
});

describe('aggregateByOwner', () => {
  const services = [
    { name: 'a', owner: { team: 'team-a' }, contractStatus: 'Compliant', blastRadius: 2, complianceScore: 100 },
    { name: 'b', owner: { team: 'team-a' }, contractStatus: 'Warning', blastRadius: 1, complianceScore: 60 },
    { name: 'c', owner: { team: 'team-b' }, contractStatus: 'NonCompliant', blastRadius: 3, complianceScore: 20 },
    { name: 'd', owner: { team: 'team-a' }, contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
    { name: 'e', owner: null, contractStatus: 'Reference', blastRadius: 0 },
    { name: 'f', owner: { dri: 'alice' }, contractStatus: 'Compliant', blastRadius: 1, complianceScore: 100 },
  ];

  it('groups by canonical owner key', () => {
    const result = aggregateByOwner(services);
    const keys = result.map((r) => r.key);
    expect(keys).toContain('team-a');
    expect(keys).toContain('team-b');
    expect(keys).toContain('(unowned)');
    expect(keys).toContain('alice');
  });

  it('counts services correctly', () => {
    const result = aggregateByOwner(services);
    const teamA = result.find((r) => r.key === 'team-a')!;
    expect(teamA.services).toBe(3); // 'a', 'b', and 'd' (structured with team: team-a)
    expect(teamA.compliant).toBe(2);
    expect(teamA.warning).toBe(1);
  });

  it('computes blast radius sum', () => {
    const result = aggregateByOwner(services);
    const teamA = result.find((r) => r.key === 'team-a')!;
    expect(teamA.totalBlast).toBe(3); // 2 + 1 + 0
  });

  it('computes compliance as the share of assessed services that are compliant', () => {
    const result = aggregateByOwner(services);
    const teamA = result.find((r) => r.key === 'team-a')!;
    // 2 compliant of 3 assessed (2 Compliant + 1 Warning) → 67
    expect(teamA.compliancePercent).toBe(67);
  });

  it('handles reference-only owner (no compliance scores)', () => {
    const result = aggregateByOwner(services);
    const unowned = result.find((r) => r.key === '(unowned)')!;
    expect(unowned.reference).toBe(1);
    expect(unowned.compliancePercent).toBe(-1); // no scores
  });

  it('returns sorted by key', () => {
    const result = aggregateByOwner(services);
    const keys = result.map((r) => r.key);
    expect(keys).toEqual([...keys].sort());
  });

  it('returns empty array for no services', () => {
    expect(aggregateByOwner([])).toEqual([]);
  });

  it('produces chart-ready segments that sum to total services', () => {
    const result = aggregateByOwner(services);
    for (const agg of result) {
      const segTotal = agg.compliant + agg.warning + agg.nonCompliant + agg.reference + agg.unknown;
      expect(segTotal).toBe(agg.services);
    }
  });

  it('produces readiness segments that sum to total services', () => {
    const result = aggregateByOwner(services);
    for (const agg of result) {
      const segTotal = agg.ready + agg.partial + agg.notReady + agg.notConfigured;
      expect(segTotal).toBe(agg.services);
    }
  });

  it('counts per-owner readiness buckets (unknown → notConfigured)', () => {
    const ready = { readiness: { score: 100, passing: true } };
    const partial = { readiness: { score: 60, passing: false } };
    const notReady = { readiness: { score: 10, passing: false } };
    const svc = [
      { name: 'a', owner: { team: 'team-r' }, contractStatus: 'Compliant', ...ready },
      { name: 'b', owner: { team: 'team-r' }, contractStatus: 'Warning', ...partial },
      { name: 'c', owner: { team: 'team-r' }, contractStatus: 'NonCompliant', ...notReady },
      { name: 'd', owner: { team: 'team-r' }, contractStatus: 'Reference' }, // no readiness → notConfigured
    ];
    const result = aggregateByOwner(svc);
    const team = result.find((r) => r.key === 'team-r')!;
    expect(team.services).toBe(4);
    expect(team.ready).toBe(1);
    expect(team.partial).toBe(1);
    expect(team.notReady).toBe(1);
    expect(team.notConfigured).toBe(1);
  });

  it('handles owner with only compliant services', () => {
    const svc = [
      { name: 'x', owner: { team: 'clean-team' }, contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
      { name: 'y', owner: { team: 'clean-team' }, contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
    ];
    const result = aggregateByOwner(svc);
    const team = result.find((r) => r.key === 'clean-team')!;
    expect(team.compliant).toBe(2);
    expect(team.warning).toBe(0);
    expect(team.nonCompliant).toBe(0);
    expect(team.reference).toBe(0);
    expect(team.compliancePercent).toBe(100);
  });

  it('handles owner with mixed statuses', () => {
    const svc = [
      { name: 'a', owner: { team: 'mixed' }, contractStatus: 'Compliant', blastRadius: 0 },
      { name: 'b', owner: { team: 'mixed' }, contractStatus: 'Warning', blastRadius: 0 },
      { name: 'c', owner: { team: 'mixed' }, contractStatus: 'NonCompliant', blastRadius: 0 },
      { name: 'd', owner: { team: 'mixed' }, contractStatus: 'Reference', blastRadius: 0 },
    ];
    const result = aggregateByOwner(svc);
    const team = result.find((r) => r.key === 'mixed')!;
    expect(team.compliant).toBe(1);
    expect(team.warning).toBe(1);
    expect(team.nonCompliant).toBe(1);
    expect(team.reference).toBe(1);
    expect(team.services).toBe(4);
  });

  it('handles owner with only reference services', () => {
    const svc = [
      { name: 'r1', owner: { team: 'ref-only' }, contractStatus: 'Reference', blastRadius: 0 },
    ];
    const result = aggregateByOwner(svc);
    const team = result.find((r) => r.key === 'ref-only')!;
    expect(team.reference).toBe(1);
    expect(team.compliant).toBe(0);
    expect(team.compliancePercent).toBe(-1);
  });
});

describe('extractOwnerDetail', () => {
  it('extracts structured owner with all fields', () => {
    const services = [
      {
        name: 'svc-a',
        owner: {
          team: 'platform',
          dri: 'alice',
          contacts: [
            { type: 'email', value: 'platform@acme.com', purpose: 'escalation' },
            { type: 'chat', value: '#platform', purpose: 'support' },
          ],
        },
      },
    ];
    const detail = extractOwnerDetail('platform', services);
    expect(detail.key).toBe('platform');
    expect(detail.team).toBe('platform');
    expect(detail.dri).toBe('alice');
    expect(detail.isStructured).toBe(true);
    expect(detail.driConflict).toBe(false);
    expect(detail.allDris).toEqual(['alice']);
    expect(detail.contacts).toHaveLength(2);
    expect(detail.contacts[0]).toEqual({ type: 'email', value: 'platform@acme.com', purpose: 'escalation' });
    expect(detail.contacts[1]).toEqual({ type: 'chat', value: '#platform', purpose: 'support' });
  });

  it('shows team from structured owner', () => {
    const detail = extractOwnerDetail('foundations', [
      { name: 'a', owner: { team: 'foundations' } },
    ]);
    expect(detail.team).toBe('foundations');
    expect(detail.isStructured).toBe(true);
  });

  it('shows DRI from structured owner', () => {
    const detail = extractOwnerDetail('alice', [
      { name: 'a', owner: { dri: 'alice' } },
    ]);
    expect(detail.dri).toBe('alice');
    expect(detail.isStructured).toBe(true);
  });

  it('shows contact purpose when present', () => {
    const detail = extractOwnerDetail('t', [
      { name: 'a', owner: { team: 't', contacts: [{ type: 'oncall', value: 'pg-team', purpose: 'oncall' }] } },
    ]);
    expect(detail.contacts[0].purpose).toBe('oncall');
  });

  it('handles empty services', () => {
    const detail = extractOwnerDetail('team/payments', []);
    expect(detail.team).toBe('');
    expect(detail.isStructured).toBe(false);
    expect(detail.driConflict).toBe(false);
    expect(detail.allDris).toEqual([]);
    expect(detail.contacts).toHaveLength(0);
  });

  it('handles multiple services with consistent structured owner', () => {
    const owner = { team: 'platform', dri: 'alice', contacts: [{ type: 'email', value: 'p@a.com' }] };
    const detail = extractOwnerDetail('platform', [
      { name: 'a', owner: { ...owner } },
      { name: 'b', owner: { ...owner } },
    ]);
    expect(detail.team).toBe('platform');
    expect(detail.dri).toBe('alice');
    expect(detail.driConflict).toBe(false);
    expect(detail.contacts).toHaveLength(1);
  });

  it('merges contacts from different services and deduplicates', () => {
    const detail = extractOwnerDetail('platform', [
      { name: 'a', owner: { team: 'platform', dri: 'alice', contacts: [
        { type: 'slack', value: '#platform-alerts' },
        { type: 'email', value: 'platform@acme.com' },
      ] } },
      { name: 'b', owner: { team: 'platform', dri: 'alice', contacts: [
        { type: 'slack', value: '#platform-alerts' },
        { type: 'pagerduty', value: 'platform-oncall' },
      ] } },
    ]);
    expect(detail.contacts).toHaveLength(3);
    expect(detail.contacts.map(c => c.value)).toEqual([
      '#platform-alerts', 'platform@acme.com', 'platform-oncall',
    ]);
    expect(detail.driConflict).toBe(false);
  });

  it('flags DRI conflict when services have different DRIs', () => {
    const detail = extractOwnerDetail('platform', [
      { name: 'a', owner: { team: 'platform', dri: 'alice' } },
      { name: 'b', owner: { team: 'platform', dri: 'bob' } },
    ]);
    expect(detail.driConflict).toBe(true);
    expect(detail.allDris).toEqual(['alice', 'bob']);
    expect(detail.dri).toBe('alice'); // first alphabetically
  });

  it('merges contacts and flags DRI conflict together', () => {
    const detail = extractOwnerDetail('platform', [
      { name: 'a', owner: { team: 'platform', dri: 'alice', contacts: [
        { type: 'slack', value: '#svc-a' },
      ] } },
      { name: 'b', owner: { team: 'platform', dri: 'bob', contacts: [
        { type: 'pagerduty', value: 'oncall-b' },
      ] } },
    ]);
    expect(detail.driConflict).toBe(true);
    expect(detail.allDris).toEqual(['alice', 'bob']);
    expect(detail.contacts).toHaveLength(2);
  });

  it('returns empty for empty services (no fallback to key)', () => {
    const detail = extractOwnerDetail('team-x', []);
    expect(detail.team).toBe('');
    expect(detail.isStructured).toBe(false);
    expect(detail.driConflict).toBe(false);
  });
});

describe('computeTooltipPosition', () => {
  // Mock window dimensions: tests use default 1200x800

  it('positions above cursor by default', () => {
    const pos = computeTooltipPosition(500, 400, 180, 100);
    expect(pos.top).toBeLessThan(400);
    // Centered horizontally
    expect(pos.left).toBeCloseTo(500 - 90, 0);
  });

  it('falls back below cursor when near top edge', () => {
    const pos = computeTooltipPosition(500, 30, 180, 100);
    // Not enough room above (30 - 100 - 8 < 8), so below
    expect(pos.top).toBeGreaterThan(30);
  });

  it('clamps to left edge', () => {
    const pos = computeTooltipPosition(20, 400, 180, 100);
    expect(pos.left).toBeGreaterThanOrEqual(8);
  });

  it('clamps to right edge', () => {
    const pos = computeTooltipPosition(1180, 400, 180, 100);
    // Should not overflow right edge (1200 - 8 = 1192)
    expect(pos.left + 180).toBeLessThanOrEqual(1200 - 8);
  });

  it('clamps to bottom edge', () => {
    // Cursor near bottom, not enough room above OR below normally
    const pos = computeTooltipPosition(500, 790, 180, 100);
    expect(pos.top + 100).toBeLessThanOrEqual(800 - 8);
  });

  it('handles large tooltip centered', () => {
    const pos = computeTooltipPosition(600, 500, 400, 200);
    expect(pos.left).toBeGreaterThanOrEqual(8);
    expect(pos.left + 400).toBeLessThanOrEqual(1200 - 8);
  });
});

describe('aggregateByOwner — sorting/filtering support', () => {
  const services = [
    { name: 'a', owner: { team: 'team-a' }, contractStatus: 'Compliant', blastRadius: 5, complianceScore: 100 },
    { name: 'b', owner: { team: 'team-a' }, contractStatus: 'Warning', blastRadius: 3, complianceScore: 50 },
    { name: 'c', owner: { team: 'team-b' }, contractStatus: 'NonCompliant', blastRadius: 10, complianceScore: 0 },
    { name: 'd', owner: { team: 'team-c' }, contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
    { name: 'e', owner: { team: 'team-c' }, contractStatus: 'Compliant', blastRadius: 1, complianceScore: 100 },
  ];

  it('supports sort by services (descending)', () => {
    const result = aggregateByOwner(services);
    const sorted = [...result].sort((a, b) => b.services - a.services);
    expect(sorted[0].key).toBe('team-a');
    expect(sorted[0].services).toBe(2);
  });

  it('supports sort by blast radius (descending)', () => {
    const result = aggregateByOwner(services);
    const sorted = [...result].sort((a, b) => b.totalBlast - a.totalBlast);
    expect(sorted[0].key).toBe('team-b');
    expect(sorted[0].totalBlast).toBe(10);
  });

  it('supports sort by compliance % (ascending)', () => {
    const result = aggregateByOwner(services);
    const sorted = [...result].sort((a, b) => a.compliancePercent - b.compliancePercent);
    // team-b: 0/1=0%, team-a: 1/2=50%, team-c: 2/2=100% (share of assessed compliant)
    expect(sorted[0].key).toBe('team-b');
    expect(sorted[0].compliancePercent).toBe(0);
  });

  it('supports filter: has warnings', () => {
    const result = aggregateByOwner(services);
    const filtered = result.filter((o) => o.warning > 0);
    expect(filtered).toHaveLength(1);
    expect(filtered[0].key).toBe('team-a');
  });

  it('supports filter: has non-compliant', () => {
    const result = aggregateByOwner(services);
    const filtered = result.filter((o) => o.nonCompliant > 0);
    expect(filtered).toHaveLength(1);
    expect(filtered[0].key).toBe('team-b');
  });

  it('supports filter: fully compliant (100%)', () => {
    const result = aggregateByOwner(services);
    const filtered = result.filter((o) => o.compliancePercent === 100);
    // team-c: 2 of 2 assessed compliant → 100%
    expect(filtered).toHaveLength(1);
    expect(filtered[0].key).toBe('team-c');
  });

  it('supports text filter by owner key', () => {
    const result = aggregateByOwner(services);
    const filtered = result.filter((o) => o.key.toLowerCase().includes('team-b'));
    expect(filtered).toHaveLength(1);
    expect(filtered[0].key).toBe('team-b');
  });
});

describe('compareScoresUnassessedLast', () => {
  const sortAsc = (xs: number[]) => [...xs].sort((a, b) => compareScoresUnassessedLast(a, b, 1));
  const sortDesc = (xs: number[]) => [...xs].sort((a, b) => compareScoresUnassessedLast(a, b, -1));

  it('keeps unassessed (-1) last when sorting ascending', () => {
    expect(sortAsc([50, -1, 0, 100])).toEqual([0, 50, 100, -1]);
  });
  it('keeps unassessed (-1) last when sorting descending', () => {
    expect(sortDesc([50, -1, 0, 100])).toEqual([100, 50, 0, -1]);
  });
  it('orders two unassessed values equally', () => {
    expect(compareScoresUnassessedLast(-1, -1, 1)).toBe(0);
  });
});

describe('versionPolicyLabel', () => {
  it('returns label for tracking', () => expect(versionPolicyLabel('tracking')).toBe('Tracking latest'));
  it('returns label for pinned-tag', () => expect(versionPolicyLabel('pinned-tag')).toBe('Pinned to tag'));
  it('returns label for pinned-digest', () => expect(versionPolicyLabel('pinned-digest')).toBe('Pinned to digest'));
  it('returns empty for undefined', () => expect(versionPolicyLabel(undefined)).toBe(''));
  it('returns empty for empty string', () => expect(versionPolicyLabel('')).toBe(''));
  it('returns raw value for unknown policy', () => expect(versionPolicyLabel('custom')).toBe('custom'));
});

describe('versionPolicyClass', () => {
  it('returns policy-tracking for tracking', () => expect(versionPolicyClass('tracking')).toBe('policy-tracking'));
  it('returns policy-tag for pinned-tag', () => expect(versionPolicyClass('pinned-tag')).toBe('policy-tag'));
  it('returns policy-digest for pinned-digest', () => expect(versionPolicyClass('pinned-digest')).toBe('policy-digest'));
  it('returns empty for undefined', () => expect(versionPolicyClass(undefined)).toBe(''));
  it('returns empty for unknown policy', () => expect(versionPolicyClass('other')).toBe(''));
});

describe('countHighImpact', () => {
  it('counts services with blastRadius >= 3', () => {
    const services = [
      { blastRadius: 5 },
      { blastRadius: 3 },
      { blastRadius: 2 },
      { blastRadius: 0 },
    ];
    expect(countHighImpact(services)).toBe(2);
  });

  it('returns 0 for empty list', () => {
    expect(countHighImpact([])).toBe(0);
  });

  it('treats missing blastRadius as 0', () => {
    expect(countHighImpact([{}, { blastRadius: undefined }])).toBe(0);
  });

  it('counts all when all are high impact', () => {
    expect(countHighImpact([{ blastRadius: 3 }, { blastRadius: 10 }])).toBe(2);
  });

  it('returns 0 when none meet threshold', () => {
    expect(countHighImpact([{ blastRadius: 1 }, { blastRadius: 2 }])).toBe(0);
  });
});

describe('filterServices', () => {
  const services = [
    { name: 'api-gateway', owner: { team: 'team-a' }, contractStatus: 'Compliant', sources: ['k8s'], blastRadius: 5 },
    { name: 'auth-service', owner: { team: 'team-a' }, contractStatus: 'Warning', sources: ['k8s', 'oci'], blastRadius: 3 },
    { name: 'payment-svc', owner: { team: 'team-b' }, contractStatus: 'NonCompliant', sources: ['oci'], blastRadius: 1 },
    { name: 'user-db', owner: { team: 'team-b' }, contractStatus: 'Compliant', sources: ['local'], blastRadius: 0 },
  ];

  it('returns all services with no filters', () => {
    expect(filterServices(services, {})).toHaveLength(4);
  });

  it('returns all with all-defaults', () => {
    expect(filterServices(services, { nameFilter: '', sourceFilter: 'all', statusFilter: 'all' })).toHaveLength(4);
  });

  it('filters by name (case-insensitive)', () => {
    const result = filterServices(services, { nameFilter: 'api' });
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('api-gateway');
  });

  it('filters by owner name match', () => {
    const result = filterServices(services, { nameFilter: 'team-b' });
    expect(result).toHaveLength(2); // payment-svc + user-db
  });

  it('filters by status', () => {
    const result = filterServices(services, { statusFilter: 'Compliant' });
    expect(result).toHaveLength(2);
  });

  it('filters by source', () => {
    const result = filterServices(services, { sourceFilter: 'oci' });
    expect(result).toHaveLength(2); // auth-service + payment-svc
  });

  it('combines name + status filters', () => {
    const result = filterServices(services, { nameFilter: 'team-a', statusFilter: 'Warning' });
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('auth-service');
  });

  it('combines all three filters', () => {
    const result = filterServices(services, { nameFilter: 'auth', sourceFilter: 'k8s', statusFilter: 'Warning' });
    expect(result).toHaveLength(1);
    expect(result[0].name).toBe('auth-service');
  });

  it('returns empty when no services match', () => {
    expect(filterServices(services, { nameFilter: 'nonexistent' })).toHaveLength(0);
  });

  it('high impact count reflects status filter', () => {
    // This is the core bug fix: high impact should change with status filter
    const allFiltered = filterServices(services, {});
    const compliantOnly = filterServices(services, { statusFilter: 'Compliant' });
    expect(countHighImpact(allFiltered)).toBe(2); // api-gateway(5) + auth-service(3)
    expect(countHighImpact(compliantOnly)).toBe(1); // only api-gateway(5) is Compliant
  });
});

describe('paginate', () => {
  const items = Array.from({ length: 23 }, (_, i) => i + 1); // 1..23

  it('returns the first page with the requested size', () => {
    const r = paginate(items, 1, 10);
    expect(r.items).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
    expect(r.page).toBe(1);
    expect(r.totalPages).toBe(3);
    expect(r.total).toBe(23);
    expect(r.perPage).toBe(10);
  });

  it('returns a middle page', () => {
    const r = paginate(items, 2, 10);
    expect(r.items).toEqual([11, 12, 13, 14, 15, 16, 17, 18, 19, 20]);
    expect(r.page).toBe(2);
  });

  it('returns a partial last page', () => {
    const r = paginate(items, 3, 10);
    expect(r.items).toEqual([21, 22, 23]);
    expect(r.page).toBe(3);
    expect(r.totalPages).toBe(3);
  });

  it('clamps a page below 1 to the first page', () => {
    const r = paginate(items, 0, 10);
    expect(r.page).toBe(1);
    expect(r.items[0]).toBe(1);
  });

  it('clamps a page beyond the range to the last page', () => {
    const r = paginate(items, 99, 10);
    expect(r.page).toBe(3);
    expect(r.items).toEqual([21, 22, 23]);
  });

  it('handles an empty list', () => {
    const r = paginate([], 1, 10);
    expect(r.items).toEqual([]);
    expect(r.totalPages).toBe(1);
    expect(r.page).toBe(1);
    expect(r.total).toBe(0);
  });

  it('handles an exact multiple of perPage', () => {
    const r = paginate(Array.from({ length: 20 }, (_, i) => i + 1), 2, 10);
    expect(r.totalPages).toBe(2);
    expect(r.items).toHaveLength(10);
  });

  it('returns everything in one page when perPage is non-positive', () => {
    const r = paginate(items, 1, 0);
    expect(r.items).toHaveLength(23);
    expect(r.totalPages).toBe(1);
    expect(r.perPage).toBe(23);
  });
});

// ── Readiness ──

const rdy = (over: Record<string, unknown> = {}) => ({
  readiness: {
    score: 100, minScore: 100, passing: true, totalWeight: 10, earnedWeight: 10,
    partialCredit: 0, expires: '2099-12-31', expired: false, daysRemaining: 9999,
    doneCount: 1, partialCount: 0, notDoneCount: 0, deferredCount: 0, checks: [], ...over,
  },
});

describe('readinessBucket', () => {
  it('returns unknown when no readiness block is declared', () => {
    expect(readinessBucket({})).toBe('unknown');
    expect(readinessBucket({ readiness: null })).toBe('unknown');
  });
  it('returns ready when the gate passes (regardless of score band)', () => {
    expect(readinessBucket(rdy({ passing: true, score: 60 }))).toBe('ready');
  });
  it('returns partial when not passing but score >= 50', () => {
    expect(readinessBucket(rdy({ passing: false, score: 50 }))).toBe('partial');
    expect(readinessBucket(rdy({ passing: false, score: 89 }))).toBe('partial');
  });
  it('returns not-ready when not passing and score < 50', () => {
    expect(readinessBucket(rdy({ passing: false, score: 49 }))).toBe('not-ready');
    expect(readinessBucket(rdy({ passing: false, score: 0 }))).toBe('not-ready');
  });
});

describe('readinessBucketLabel', () => {
  it('maps each bucket to a human label', () => {
    expect(readinessBucketLabel('ready')).toBe('Ready');
    expect(readinessBucketLabel('partial')).toBe('Partial');
    expect(readinessBucketLabel('not-ready')).toBe('Not Ready');
    expect(readinessBucketLabel('unknown')).toBe('Not configured');
  });
});

describe('readinessBucketClass', () => {
  it('reuses the shared status palette', () => {
    expect(readinessBucketClass('ready')).toBe('badge-ok');
    expect(readinessBucketClass('partial')).toBe('badge-warn');
    expect(readinessBucketClass('not-ready')).toBe('badge-err');
    expect(readinessBucketClass('unknown')).toBe('badge-neutral');
  });
});

describe('readinessGateClass', () => {
  it('is green when passing the gate, regardless of absolute score', () => {
    // 70% passing a minScore-70 gate should read green, not amber.
    expect(readinessGateClass({ score: 70, minScore: 70, passing: true, expired: false } as any)).toBe('score-ok');
  });
  it('is NOT green when below the gate even at a high score', () => {
    // 79% missing an 85 gate must not read green.
    const cls = readinessGateClass({ score: 79, minScore: 85, passing: false, expired: false } as any);
    expect(cls).not.toBe('score-ok');
    expect(cls).toBe('score-warn'); // within striking distance (>= 50)
  });
  it('is red when far below the gate', () => {
    expect(readinessGateClass({ score: 30, minScore: 80, passing: false, expired: false } as any)).toBe('score-err');
  });
  it('is red when expired even if the score would otherwise pass', () => {
    expect(readinessGateClass({ score: 90, minScore: 80, passing: false, expired: true } as any)).toBe('score-err');
  });
  it('returns empty for no readiness block', () => {
    expect(readinessGateClass(null)).toBe('');
    expect(readinessGateClass(undefined)).toBe('');
  });
  it('distinguishes two close scores by their gate (the regression this fixes)', () => {
    // fraud-service 79% passing minScore 75 vs payments-service 70% below minScore 80.
    expect(readinessGateClass({ score: 79, minScore: 75, passing: true, expired: false } as any)).toBe('score-ok');
    expect(readinessGateClass({ score: 70, minScore: 80, passing: false, expired: false } as any)).toBe('score-warn');
  });
});

describe('readinessGateTip', () => {
  it('reads "passing" with the minScore when the gate is met', () => {
    expect(readinessGateTip({ score: 79, minScore: 75, passing: true, expired: false } as any))
      .toBe('79% — passing (minScore 75)');
  });
  it('reads "below gate" with the minScore when the gate is missed', () => {
    expect(readinessGateTip({ score: 70, minScore: 80, passing: false, expired: false } as any))
      .toBe('70% — below gate (minScore 80)');
  });
  it('reads "expired" when the assessment has expired', () => {
    expect(readinessGateTip({ score: 90, minScore: 80, passing: false, expired: true } as any))
      .toBe('90% — expired (minScore 80)');
  });
  it('returns empty for no readiness block', () => {
    expect(readinessGateTip(null)).toBe('');
  });
});

describe('summarizeReadiness', () => {
  it('handles an empty service list', () => {
    const s = summarizeReadiness([]);
    expect(s.total).toBe(0);
    expect(s.configured).toBe(0);
    expect(s.avgScore).toBe(-1);
  });
  it('counts services with no readiness as not configured and excludes them from avg', () => {
    const s = summarizeReadiness([{}, { readiness: null }]);
    expect(s.total).toBe(2);
    expect(s.notConfigured).toBe(2);
    expect(s.configured).toBe(0);
    expect(s.avgScore).toBe(-1);
  });
  it('buckets, sums check counts, and averages score over configured only', () => {
    const services = [
      rdy({ passing: true, score: 100, doneCount: 2, partialCount: 0, notDoneCount: 0, deferredCount: 0, expired: false, checks: [{}, {}] }),
      rdy({ passing: false, score: 60, doneCount: 1, partialCount: 1, notDoneCount: 0, deferredCount: 0, expired: true, checks: [{}, {}] }),
      rdy({ passing: false, score: 20, doneCount: 0, partialCount: 0, notDoneCount: 1, deferredCount: 1, expired: true, checks: [{}, {}] }),
      {}, // not configured
    ];
    const s = summarizeReadiness(services);
    expect(s.total).toBe(4);
    expect(s.ready).toBe(1);
    expect(s.partial).toBe(1);
    expect(s.notReady).toBe(1);
    expect(s.notConfigured).toBe(1);
    expect(s.configured).toBe(3);
    expect(s.avgScore).toBe(60); // round((100+60+20)/3)
    expect(s.expiredAssessments).toBe(2);
    expect(s.totalDone).toBe(3);
    expect(s.totalPartial).toBe(1);
    expect(s.totalNotDone).toBe(1);
    expect(s.totalDeferred).toBe(1);
  });
  it('tolerates missing optional count fields', () => {
    const s = summarizeReadiness([{ readiness: { score: 100, passing: true, checks: [{}], expires: '2099-12-31', expired: false } } as never]);
    expect(s.configured).toBe(1);
    expect(s.totalDone).toBe(0);
    expect(s.totalPartial).toBe(0);
    expect(s.totalNotDone).toBe(0);
    expect(s.totalDeferred).toBe(0);
  });
});

describe('isUrlEvidence', () => {
  it('recognizes http(s) URLs', () => {
    expect(isUrlEvidence('https://grafana.example.com/d/x')).toBe(true);
    expect(isUrlEvidence('http://example.com')).toBe(true);
    expect(isUrlEvidence('HTTPS://EXAMPLE.COM')).toBe(true);
  });
  it('rejects non-URL evidence and empties', () => {
    expect(isUrlEvidence('docs/runbooks/x.md')).toBe(false);
    expect(isUrlEvidence('SEC-1842')).toBe(false);
    expect(isUrlEvidence('')).toBe(false);
    expect(isUrlEvidence(null)).toBe(false);
    expect(isUrlEvidence(undefined)).toBe(false);
  });
});

describe('readinessCheckTypes', () => {
  it('returns sorted unique evidence types present across services', () => {
    const services = [
      rdy({ checks: [{ type: 'url' }, { type: 'document' }] }),
      rdy({ checks: [{ type: 'url' }, { type: 'ticket' }] }),
      {},
    ];
    expect(readinessCheckTypes(services)).toEqual(['document', 'ticket', 'url']);
  });
  it('returns [] when no service declares readiness', () => {
    expect(readinessCheckTypes([{}, { readiness: null }])).toEqual([]);
  });
});

describe('summarizeFleet', () => {
  it('handles an empty list', () => {
    const s = summarizeFleet([]);
    expect(s.total).toBe(0);
    expect(s.assessed).toBe(0);
    expect(s.compliancePercent).toBe(-1);
    expect(s.needsAttention).toBe(0);
    expect(s.highImpact).toBe(0);
  });
  it('counts statuses, attention, compliance % over assessed, and high impact', () => {
    const services = [
      { contractStatus: 'Compliant', blastRadius: 5 },
      { contractStatus: 'Compliant', blastRadius: 1 },
      { contractStatus: 'Warning', blastRadius: 3 },
      { contractStatus: 'NonCompliant', blastRadius: 0 },
      { contractStatus: 'Reference' },
      { contractStatus: 'Unknown' },
    ];
    const s = summarizeFleet(services);
    expect(s.total).toBe(6);
    expect(s.compliant).toBe(2);
    expect(s.warning).toBe(1);
    expect(s.nonCompliant).toBe(1);
    expect(s.reference).toBe(1);
    expect(s.unknown).toBe(1);
    expect(s.assessed).toBe(4); // compliant + warning + nonCompliant
    expect(s.needsAttention).toBe(2); // warning + nonCompliant
    expect(s.compliancePercent).toBe(50); // 2/4
    expect(s.highImpact).toBe(2); // blast >= 3
  });
  it('returns compliancePercent -1 when nothing is assessed (reference/unknown only)', () => {
    const s = summarizeFleet([{ contractStatus: 'Reference' }, { contractStatus: 'Unknown' }]);
    expect(s.assessed).toBe(0);
    expect(s.compliancePercent).toBe(-1);
    expect(s.needsAttention).toBe(0);
  });
});

describe('summarize', () => {
  it('handles an empty list', () => {
    const m = summarize([]);
    expect(m.total).toBe(0);
    expect(m.assessed).toBe(0);
    expect(m.compliancePercent).toBe(-1);
    expect(m.highImpact).toBe(0);
    expect(m.readiness.total).toBe(0);
    expect(m.byOwner).toEqual([]);
    expect(m.byCategory).toEqual([]);
  });

  it('computes all KPIs in a single pass', () => {
    const services = [
      { name: 'a', owner: { team: 'team-a' }, contractStatus: 'Compliant', blastRadius: 5, complianceScore: 100, readiness: { ...rdy().readiness, passing: true, score: 100, doneCount: 2, partialCount: 0, notDoneCount: 0, deferredCount: 0, expired: false } },
      { name: 'b', owner: { team: 'team-a' }, contractStatus: 'Warning', blastRadius: 1, complianceScore: 60, readiness: { ...rdy().readiness, passing: false, score: 60, doneCount: 1, partialCount: 1, notDoneCount: 0, deferredCount: 0, expired: true } },
      { name: 'c', owner: { team: 'team-b' }, contractStatus: 'NonCompliant', blastRadius: 3, complianceScore: 20, readiness: { ...rdy().readiness, passing: false, score: 20, doneCount: 0, partialCount: 0, notDoneCount: 2, deferredCount: 0, expired: false } },
      { name: 'd', owner: null, contractStatus: 'Reference', blastRadius: 0 }, // no readiness
    ];
    const m = summarize(services);

    // Fleet KPIs
    expect(m.total).toBe(4);
    expect(m.compliant).toBe(1);
    expect(m.warning).toBe(1);
    expect(m.nonCompliant).toBe(1);
    expect(m.reference).toBe(1);
    expect(m.assessed).toBe(3);
    expect(m.needsAttention).toBe(2);
    expect(m.compliancePercent).toBe(33); // 1/3
    expect(m.highImpact).toBe(2); // >= 3

    // Readiness
    expect(m.readiness.total).toBe(4);
    expect(m.readiness.configured).toBe(3);
    expect(m.readiness.notConfigured).toBe(1);
    expect(m.readiness.ready).toBe(1);
    expect(m.readiness.partial).toBe(1);
    expect(m.readiness.notReady).toBe(1);
    expect(m.readiness.avgScore).toBe(60); // (100+60+20)/3
    expect(m.readiness.expiredAssessments).toBe(1);
    expect(m.readiness.totalDone).toBe(3);
    expect(m.readiness.totalPartial).toBe(1);
    expect(m.readiness.totalNotDone).toBe(2);
    expect(m.readiness.totalDeferred).toBe(0);

    // Owner aggregation
    expect(m.byOwner).toHaveLength(3); // team-a, team-b, (unowned)
    const teamA = m.byOwner.find((o) => o.key === 'team-a')!;
    expect(teamA.services).toBe(2);
    expect(teamA.compliant).toBe(1);
    expect(teamA.warning).toBe(1);
    expect(teamA.totalBlast).toBe(6);
    expect(teamA.compliancePercent).toBe(50); // 1 compliant of 2 assessed
    // Readiness composition: a passes (ready), b score 60 not passing (partial).
    expect(teamA.ready).toBe(1);
    expect(teamA.partial).toBe(1);
    expect(teamA.notReady).toBe(0);
    expect(teamA.notConfigured).toBe(0);

    // Unowned d has no readiness block → notConfigured.
    const unowned = m.byOwner.find((o) => o.key === '(unowned)')!;
    expect(unowned.notConfigured).toBe(1);
    expect(unowned.ready).toBe(0);
  });

  it('aggregates by category with other bucket', () => {
    const services = [
      {
        name: 'a',
        owner: { team: 'team-a' },
        contractStatus: 'Compliant',
        readiness: {
          score: 100, minScore: 100, passing: true, totalWeight: 10, earnedWeight: 10,
          partialCredit: 0, expires: '2099-12-31', expired: false,
          doneCount: 3, partialCount: 1, notDoneCount: 0, deferredCount: 1,
          checks: [
            { id: '1', type: 'url', category: 'security', status: 'done', weight: 1, earnedWeight: 1, excluded: false },
            { id: '2', type: 'doc', category: 'security', status: 'partial', weight: 1, earnedWeight: 0.5, excluded: false },
            { id: '3', type: 'url', category: 'performance', status: 'done', weight: 1, earnedWeight: 1, excluded: false },
            { id: '4', type: 'doc', status: 'done', weight: 1, earnedWeight: 1, excluded: false }, // no category → other
            { id: '5', type: 'url', status: 'deferred', weight: 0, earnedWeight: 0, excluded: true }, // no category → other
          ],
        },
      },
    ];
    const m = summarize(services);
    expect(m.byCategory).toHaveLength(3); // security, performance, other
    const security = m.byCategory.find((c) => c.category === 'security')!;
    expect(security.checks).toBe(2);
    expect(security.done).toBe(1);
    expect(security.partial).toBe(1);
    expect(security.notDone).toBe(0);
    expect(security.deferred).toBe(0);

    const performance = m.byCategory.find((c) => c.category === 'performance')!;
    expect(performance.checks).toBe(1);
    expect(performance.done).toBe(1);

    const other = m.byCategory.find((c) => c.category === 'other')!;
    expect(other.checks).toBe(2);
    expect(other.done).toBe(1);
    expect(other.deferred).toBe(1);
  });

  it('produces sorted byOwner and byCategory', () => {
    const services = [
      { name: 'a', owner: { team: 'zebra' }, contractStatus: 'Compliant', readiness: { ...rdy().readiness, checks: [{ id: '1', type: 'url', category: 'zzz', status: 'done', weight: 1, earnedWeight: 1, excluded: false }] } },
      { name: 'b', owner: { team: 'alpha' }, contractStatus: 'Compliant', readiness: { ...rdy().readiness, checks: [{ id: '2', type: 'url', category: 'aaa', status: 'done', weight: 1, earnedWeight: 1, excluded: false }] } },
    ];
    const m = summarize(services);
    expect(m.byOwner[0].key).toBe('alpha');
    expect(m.byOwner[1].key).toBe('zebra');
    expect(m.byCategory[0].category).toBe('aaa');
    expect(m.byCategory[1].category).toBe('zzz');
  });
});

// ── Lock and drift helpers ──

describe('shortDigest', () => {
  it('strips sha256: prefix and truncates to 8 chars by default', () => {
    expect(shortDigest('sha256:abcdef1234567890')).toBe('abcdef12');
  });
  it('accepts custom truncation length', () => {
    expect(shortDigest('sha256:abcdef1234567890', 4)).toBe('abcd');
    expect(shortDigest('sha256:abcdef1234567890', 16)).toBe('abcdef1234567890');
  });
  it('handles digest without sha256: prefix', () => {
    expect(shortDigest('abcdef1234567890')).toBe('abcdef12');
  });
  it('returns empty string for null', () => expect(shortDigest(null)).toBe(''));
  it('returns empty string for undefined', () => expect(shortDigest(undefined)).toBe(''));
  it('returns empty string for empty string', () => expect(shortDigest('')).toBe(''));
  it('truncates shorter digest gracefully', () => {
    expect(shortDigest('sha256:abc', 8)).toBe('abc');
  });
});

describe('driftBadgeClass', () => {
  it('maps locked to badge-ok', () => expect(driftBadgeClass('locked')).toBe('badge-ok'));
  it('maps drift to badge-warn', () => expect(driftBadgeClass('drift')).toBe('badge-warn'));
  it('maps unlocked to badge-neutral', () => expect(driftBadgeClass('unlocked')).toBe('badge-neutral'));
  it('maps unknown to badge-neutral', () => expect(driftBadgeClass('unknown')).toBe('badge-neutral'));
  it('returns badge-neutral for null', () => expect(driftBadgeClass(null)).toBe('badge-neutral'));
  it('returns badge-neutral for undefined', () => expect(driftBadgeClass(undefined)).toBe('badge-neutral'));
  it('returns badge-neutral for empty string', () => expect(driftBadgeClass('')).toBe('badge-neutral'));
});

describe('driftBadgeLabel', () => {
  it('maps locked to Locked', () => expect(driftBadgeLabel('locked')).toBe('Locked'));
  it('maps drift to Drift', () => expect(driftBadgeLabel('drift')).toBe('Drift'));
  it('maps unlocked to Unlocked', () => expect(driftBadgeLabel('unlocked')).toBe('Unlocked'));
  it('returns empty for unknown', () => expect(driftBadgeLabel('unknown')).toBe(''));
  it('returns empty for null', () => expect(driftBadgeLabel(null)).toBe(''));
  it('returns empty for undefined', () => expect(driftBadgeLabel(undefined)).toBe(''));
  it('returns empty for empty string', () => expect(driftBadgeLabel('')).toBe(''));
});
