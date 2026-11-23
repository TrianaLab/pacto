import { describe, it, expect } from 'vitest';
import { linkWords, linkTip } from './clusterLink.ts';
import type { ClusterSummary } from './api.ts';

const ISS = 'https://login.example/realms/eng';
const c = (link?: ClusterSummary['link']): ClusterSummary => ({ id: 'u', name: 'eu-prod', status: 'available', targets: 1, ...(link ? { link } : {}) });

describe('linkWords', () => {
  it('names the channel and each issuer model', () => {
    expect(linkWords(c({ channel: 'live', issuers: [{ issuer: ISS, model: 'shared-issuer' }] }), false)).toBe('live · shared issuer');
    expect(linkWords(c({ channel: 'live', issuers: [{ issuer: ISS, model: 'group-mapping' }] }), false)).toBe('live · group mapping');
    expect(linkWords(c({ channel: 'live', issuers: [{ issuer: ISS, model: 'issuer-not-accepted' }] }), false)).toBe('live · issuer not accepted');
    expect(linkWords(c({ channel: 'live' }), false)).toBe('live');
    expect(linkWords(c({ channel: 'live', issuers: [{ issuer: ISS, model: 'shared-issuer' }, { issuer: 'b', model: 'shared-issuer' }, { issuer: 'c', model: 'group-mapping' }] }), false))
      .toBe('live · shared issuer · group mapping');
  });

  it('says down for a dropped channel, whatever model it had', () => {
    expect(linkWords(c({ channel: 'down', issuers: [{ issuer: ISS, model: 'group-mapping' }] }), true)).toBe('down');
  });

  it('says no live channel for an approved member with no link, and nothing otherwise', () => {
    expect(linkWords(c(), true)).toBe('no live channel');
    expect(linkWords(c(), false)).toBe('');
  });
});

describe('linkTip', () => {
  it('holds the issuer, the mapping approver and date and since, never in the words', () => {
    const s = c({ channel: 'live', since: '2026-11-20T08:15:00Z', issuers: [
      { issuer: ISS, model: 'shared-issuer' },
      { issuer: 'https://b', model: 'group-mapping', mappedBy: 'oidc:alice@acme.com', mappedAt: '2026-11-19T10:00:00Z', mapping: 'eng=readers' },
      { issuer: 'https://c', model: 'issuer-not-accepted' },
    ] });
    expect(linkTip(s, false)).toBe(` Live channel since 2026-11-20 08:15. ${ISS}: its tokens are accepted.`
      + ' https://b: reads use the group mapping eng=readers approved by oidc:alice@acme.com on 2026-11-19.'
      + ' https://c: not accepted and no group mapping is approved, so its people are not reviewed here.');
    expect(linkWords(s, false)).not.toContain(ISS);
  });

  it('dates a dropped channel and explains an absent one', () => {
    expect(linkTip(c({ channel: 'down', since: '2026-11-20T08:15:00Z' }), true)).toBe(' The channel dropped at 2026-11-20 08:15; reviews here wait until the member dials again.');
    expect(linkTip(c({ channel: 'down', issuers: [{ issuer: ISS, model: 'shared-issuer' }] }), false))
      .toBe(` The channel dropped; reviews here wait until the member dials again. When it was live: ${ISS}: its tokens are accepted.`);
    expect(linkTip(c(), true)).toBe(' This member has no live channel to this hub, so signed-in people are not reviewed there.');
    expect(linkTip(c(), false)).toBe('');
  });
});
