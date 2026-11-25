import { describe, it, expect } from 'vitest';
import { boundsText, closestText, createFieldError, createQueryOf, createReturn, createSignUrl, ENV_FIELD, fieldKey, offeringPicks, placeRefusals, signatureText, type OfferingView } from './createService.ts';

const PG: OfferingView = {
  offering: 'postgresql', version: '16.1.0', requested: true, classes: ['small', 'large'],
  parameters: [
    { path: 'storageGi', type: 'integer', tier: 'tunable', minimum: 10, maximum: 500 },
    { path: 'class', type: 'string', tier: 'contract', enum: ['small', 'large'] },
    { path: 'backup.days', type: 'integer', tier: 'contract' },
    { path: 'flag', type: 'boolean', tier: 'contract' },
  ],
};
const REDIS: OfferingView = { offering: 'redis', parameters: [{ path: 'size', type: 'integer', tier: 'contract' }, { path: 'storageGi', type: 'integer', tier: 'contract' }] };

describe('placeRefusals', () => {
  it('puts each refusal at its field and keeps the rest', () => {
    const refusals = [
      { code: 'ParameterOutOfRange', message: 'm', field: 'configurations[database].values.storageGi', closest: 500, mcp: '{"offerings":[{"offering":"postgresql","values":{"storageGi":500}}]}' },
      { code: 'ParameterNotDeclared', message: 'm', field: 'configurations[cache].values.size' },
      { code: 'TierNotTunable', message: 'contract tier', field: 'environments[prod].database.class' },
      { code: 'TierNotTunable', message: 'm', field: 'environments[prod].database.storageGi', mcp: '{"offerings":[{"offering":"postgresql"}]}' },
      { code: 'EnvironmentNotDeclared', message: 'm', field: 'environments[qa]' },
      { code: 'OfferingNotConsumable', message: 'absent', field: 'offerings[mysql]' },
      { code: 'ParameterNotDeclared', message: 'm', field: 'offerings[redis]' },
      { code: 'ParameterOutOfRange', message: 'ambiguous', field: 'configurations[x].values.storageGi', mcp: 'not json' },
      { code: 'ParameterSensitive', message: 'whole', field: 'configurations[database]' },
    ];
    const { at, rest } = placeRefusals(refusals, [PG, REDIS]);
    expect(at[fieldKey('postgresql', 'storageGi')].map((r) => r.code)).toEqual(['ParameterOutOfRange']);
    expect(at[fieldKey('redis', 'size')].map((r) => r.code)).toEqual(['ParameterNotDeclared']);
    // A per-environment input exists only for a tunable field: a contract-tier one is in rest.
    expect(at[fieldKey('postgresql', 'storageGi', '*')].map((r) => r.code)).toEqual(['TierNotTunable']);
    expect(at[fieldKey('postgresql', 'class', '*')]).toBeUndefined();
    expect(at[ENV_FIELD].map((r) => r.code)).toEqual(['EnvironmentNotDeclared']);
    expect(at[fieldKey('redis', '')].map((r) => r.code)).toEqual(['ParameterNotDeclared']);
    expect(rest.map((r) => r.message)).toEqual(['contract tier', 'absent', 'ambiguous', 'whole']);
    // With no tunable field there is no environment input either.
    expect(placeRefusals([{ code: 'EnvironmentNotDeclared', message: 'm', field: 'environments[qa]' }], [REDIS]).rest).toHaveLength(1);
    expect(placeRefusals(null, []).rest).toEqual([]);
  });

  it('reads a fragment that names no offering as no offering', () => {
    const { rest } = placeRefusals([{ code: 'X', message: 'm', field: 'configurations[a].values.zzz', mcp: '{"offerings":[{}]}' }], [PG]);
    expect(rest).toHaveLength(1);
  });
});

describe('createQueryOf', () => {
  it('reads a signing return and a draft review link apart', () => {
    expect(createQueryOf({ create: '1', signed: 'sha256:i1', draft: 'draft-9' })).toEqual({ signed: 'sha256:i1', name: '', draft: '', repository: '' });
    expect(createQueryOf({ create: 'invoices', draft: 'draft-1', repository: 'r/team/invoices' })).toEqual({ signed: '', name: 'invoices', draft: 'draft-1', repository: 'r/team/invoices' });
    expect(createQueryOf({})).toEqual({ signed: '', name: '', draft: '', repository: '' });
  });
});

describe('createReturn and createSignUrl', () => {
  it('come back to the golden path page with the panel open and the intent', () => {
    expect(createReturn('ghcr.io/acme/http-service', 'sha256:i1')).toBe('#/fleet/services/ghcr.io%2Facme%2Fhttp-service?create=1&signed=sha256%3Ai1');
    const u = new URL(createSignUrl('t k', 'ghcr.io/acme/http-service', 'sha256:i1'), 'https://x');
    expect(u.pathname).toBe('/auth/sign');
    expect(u.searchParams.get('ticket')).toBe('t k');
    expect(u.searchParams.get('return')).toBe('/#/fleet/services/ghcr.io%2Facme%2Fhttp-service?create=1&signed=sha256%3Ai1');
  });
});

describe('createFieldError', () => {
  it('names the first wrong essential field', () => {
    expect(createFieldError('', '', '').field).toBe('name');
    expect(createFieldError('Bad_Name', '', 'ns').field).toBe('name');
    expect(createFieldError('ok', 'bad team!', 'ns').field).toBe('owner');
    expect(createFieldError('ok', '', '').field).toBe('namespace');
    expect(createFieldError('ok', 'billing', 'Team_B').field).toBe('namespace');
    expect(createFieldError('ok', 'billing', 'team-b')).toEqual({ field: '', message: '' });
  });
});

describe('boundsText', () => {
  it('says the values, the range or the type', () => {
    expect(boundsText({ path: 'a', tier: 'contract', enum: ['small', 2], enumTotal: 4 })).toBe('small | 2 (+2 more)');
    expect(boundsText({ path: 'a', tier: 'contract', enum: ['x'] })).toBe('x');
    expect(boundsText({ path: 'a', tier: 'contract', minimum: 10, maximum: 500 })).toBe('10-500');
    expect(boundsText({ path: 'a', tier: 'contract', exclusiveMinimum: 0 })).toBe('at least 0');
    expect(boundsText({ path: 'a', tier: 'contract', maximum: 9 })).toBe('at most 9');
    expect(boundsText({ path: 'a', tier: 'contract', maxLength: 40 })).toBe('text, at most 40');
    expect(boundsText({ path: 'a', tier: 'contract', type: 'boolean' })).toBe('boolean');
    expect(boundsText({ path: 'a', tier: 'contract' })).toBe('any value');
  });
});

describe('closestText', () => {
  it('names the closest value and what else is allowed', () => {
    expect(closestText({ code: 'ParameterOutOfRange', message: '', field: '', closest: 500 })).toBe('closest 500');
    expect(closestText({ code: 'ClassNotOffered', message: '', field: '', closest: 'small', allowed: ['small', 'large'] })).toBe('closest small (also large)');
    expect(closestText({ code: 'ParameterSensitive', message: '', field: '' })).toBe('');
  });
});

describe('offeringPicks', () => {
  it('types each value by its field and nests dotted paths', () => {
    const picks = offeringPicks([PG, REDIS], {
      postgresql: { storageGi: '20', class: 'large', 'backup.days': '7', flag: 'true' },
      redis: { size: '' },
      extra: { note: '16' },
    }, 'prod', { postgresql: { storageGi: 'lots' } });
    expect(picks).toEqual([
      { offering: 'postgresql', values: { storageGi: 20, class: 'large', backup: { days: 7 }, flag: true }, environments: { prod: { storageGi: 'lots' } } },
      { offering: 'extra', values: { note: '16' } },
    ]);
  });

  it('keeps an enum value of another JSON type and drops environments without a name', () => {
    const v: OfferingView = { offering: 'o', parameters: [{ path: 'n', tier: 'contract', enum: [1, 2] }] };
    expect(offeringPicks([v], { o: { n: '2' } }, '', { o: { n: '1' } })).toEqual([{ offering: 'o', values: { n: 2 } }]);
    expect(offeringPicks([v], {}, 'prod', { o: { n: '1' } })).toEqual([{ offering: 'o', environments: { prod: { n: 1 } } }]);
    expect(offeringPicks([PG], { postgresql: { flag: 'maybe' } }, '', {})).toEqual([{ offering: 'postgresql', values: { flag: 'maybe' } }]);
  });
});

describe('signatureText', () => {
  it('reads an unchecked path as Unknown', () => {
    expect(signatureText({ status: 'done' })).toBe('signed');
    expect(signatureText({ status: 'failed' })).toBe('signature refused');
    expect(signatureText({ status: 'unknown' })).toBe('signature Unknown');
    expect(signatureText(null)).toBe('signature Unknown');
  });
});
