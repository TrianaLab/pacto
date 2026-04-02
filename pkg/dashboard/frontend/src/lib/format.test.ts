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
  ownerDisplay,
  ownerKey,
  ownerTeam,
  ownerMatchesFilter,
  ownerIsStructured,
  aggregateByOwner,
  extractOwnerDetail,
  computeTooltipPosition,
  versionPolicyLabel,
  versionPolicyClass,
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

describe('ownerDisplay', () => {
  it('returns empty for null', () => expect(ownerDisplay(null)).toBe(''));
  it('returns empty for undefined', () => expect(ownerDisplay(undefined)).toBe(''));
  it('returns string as-is', () => expect(ownerDisplay('team/payments')).toBe('team/payments'));
  it('returns team from structured', () => expect(ownerDisplay({ team: 'foundations' })).toBe('foundations'));
  it('returns dri when no team', () => expect(ownerDisplay({ dri: 'alice' })).toBe('alice'));
  it('returns empty for empty object', () => expect(ownerDisplay({})).toBe(''));
  it('prefers team over dri', () => expect(ownerDisplay({ team: 't', dri: 'd' })).toBe('t'));
});

describe('ownerKey', () => {
  it('is the same function as ownerDisplay', () => expect(ownerKey).toBe(ownerDisplay));
});

describe('ownerTeam', () => {
  it('returns string owner as team', () => expect(ownerTeam('team/x')).toBe('team/x'));
  it('returns team from structured', () => expect(ownerTeam({ team: 'a' })).toBe('a'));
  it('returns empty for null', () => expect(ownerTeam(null)).toBe(''));
});

describe('ownerMatchesFilter', () => {
  it('matches string owner', () => expect(ownerMatchesFilter('team/payments', 'pay')).toBe(true));
  it('no match string owner', () => expect(ownerMatchesFilter('team/payments', 'xyz')).toBe(false));
  it('matches structured team', () => expect(ownerMatchesFilter({ team: 'foundations' }, 'found')).toBe(true));
  it('matches structured dri', () => expect(ownerMatchesFilter({ dri: 'alice' }, 'ali')).toBe(true));
  it('matches structured contacts', () => {
    expect(ownerMatchesFilter({ contacts: [{ value: 'alice@acme.com' }] }, 'acme')).toBe(true);
  });
  it('case-insensitive', () => expect(ownerMatchesFilter('TEAM', 'team')).toBe(true));
  it('returns false for null', () => expect(ownerMatchesFilter(null, 'x')).toBe(false));
});

describe('ownerIsStructured', () => {
  it('returns false for null', () => expect(ownerIsStructured(null)).toBe(false));
  it('returns false for string', () => expect(ownerIsStructured('str')).toBe(false));
  it('returns true for object', () => expect(ownerIsStructured({ team: 'x' })).toBe(true));
});

describe('aggregateByOwner', () => {
  const services = [
    { name: 'a', owner: 'team-a', contractStatus: 'Compliant', blastRadius: 2, complianceScore: 100 },
    { name: 'b', owner: 'team-a', contractStatus: 'Warning', blastRadius: 1, complianceScore: 60 },
    { name: 'c', owner: 'team-b', contractStatus: 'NonCompliant', blastRadius: 3, complianceScore: 20 },
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

  it('computes compliance as average of service scores', () => {
    const result = aggregateByOwner(services);
    const teamA = result.find((r) => r.key === 'team-a')!;
    // avg(100, 60, 100) = 86.67 → 87
    expect(teamA.compliancePercent).toBe(87);
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

  it('handles owner with only compliant services', () => {
    const svc = [
      { name: 'x', owner: 'clean-team', contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
      { name: 'y', owner: 'clean-team', contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
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
      { name: 'a', owner: 'mixed', contractStatus: 'Compliant', blastRadius: 0 },
      { name: 'b', owner: 'mixed', contractStatus: 'Warning', blastRadius: 0 },
      { name: 'c', owner: 'mixed', contractStatus: 'NonCompliant', blastRadius: 0 },
      { name: 'd', owner: 'mixed', contractStatus: 'Reference', blastRadius: 0 },
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
      { name: 'r1', owner: 'ref-only', contractStatus: 'Reference', blastRadius: 0 },
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

  it('handles legacy string owner', () => {
    const detail = extractOwnerDetail('team/payments', [
      { name: 'a', owner: 'team/payments' },
    ]);
    expect(detail.team).toBe('team/payments');
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

  it('returns key as team for empty services', () => {
    const detail = extractOwnerDetail('team-x', []);
    expect(detail.team).toBe('team-x');
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
    { name: 'a', owner: 'team-a', contractStatus: 'Compliant', blastRadius: 5, complianceScore: 100 },
    { name: 'b', owner: 'team-a', contractStatus: 'Warning', blastRadius: 3, complianceScore: 50 },
    { name: 'c', owner: 'team-b', contractStatus: 'NonCompliant', blastRadius: 10, complianceScore: 0 },
    { name: 'd', owner: 'team-c', contractStatus: 'Compliant', blastRadius: 0, complianceScore: 100 },
    { name: 'e', owner: 'team-c', contractStatus: 'Compliant', blastRadius: 1, complianceScore: 100 },
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
    // team-b: avg(0)=0%, team-a: avg(100,50)=75%, team-c: avg(100,100)=100%
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
    // team-c: avg(100,100)=100%
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
