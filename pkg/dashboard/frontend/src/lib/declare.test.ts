/** The Declare panel's pure words and links (513-S6). */
import { describe, it, expect } from 'vitest';
import { rangeRows, draftLine, draftMark, draftAsker, signUrl, signReturn, acceptSignUrl, isTemplateKey, declareTarget, moved, claimId, claimFieldError, offeringFieldError, openPaths } from './declare.ts';

const plan = { version: '0.1.0', deploy: 'none', ranges: [
  { dependency: 'ledger', compatibility: 'unknown', proposed: '^2.3.0', observed: '2.3.1' },
  { dependency: 'audit', compatibility: 'unknown' },
] };

describe('rangeRows', () => {
  it('keeps unknown until a proposal is checked, and never offers a range nothing proposes', () => {
    const [ledger, audit] = rangeRows(plan, []);
    expect(ledger).toMatchObject({ label: 'ledger range', value: 'keep unknown (proposed ^2.3.0)', checked: false, acceptable: true });
    expect(ledger.mark).toBe('Proposed ^2.3.0 by the cluster read, seen at 2.3.1; unchecked keeps unknown');
    expect(audit).toMatchObject({ value: 'unknown', acceptable: false, checked: false });
    expect(audit.mark).toBe('Nothing proposes a range for audit, so it stays unknown');
    const [checked] = rangeRows(plan, ['ledger']);
    expect(checked).toMatchObject({ value: '^2.3.0', checked: true });
    expect(checked.mark).toContain('Accepted: you declare ^2.3.0');
    expect(rangeRows(plan, ['audit'])[1].checked).toBe(false);
    expect(rangeRows(null, [])).toEqual([]);
  });
});

describe('drafts', () => {
  it('says an unsigned draft only asserts who it was for, and a signed one names its signer', () => {
    const unsigned = { tag: 'draft-3', state: 'draft', asker: { class: 'agent' }, onBehalfOf: 'ana', requested: ['audit ^1.4.0'], signed: false };
    expect(draftLine(unsigned)).toBe('draft-3 by an agent for ana, requests audit ^1.4.0');
    expect(draftMark(unsigned)).toBe('Asserted by the proposer, unsigned: nobody proved ana asked for it');
    expect(draftMark({ ...unsigned, onBehalfOf: '' })).toBe('Asserted by the proposer, unsigned');
    const signed = { tag: 'draft-2', state: 'draft', asker: { subject: 'bob@acme.com', issuer: 'https://idp', class: 'human' }, signed: true };
    expect(draftLine(signed)).toBe('draft-2 by bob@acme.com');
    expect(draftMark(signed)).toBe('Signed by bob@acme.com (https://idp)');
    expect(draftMark({ tag: 'draft-9', state: 'draft', signed: true })).toBe('Signed by an unnamed signer');
    expect(draftAsker({ tag: 'd', state: 'draft', asker: { class: 'human' } })).toBe('a person');
    expect(draftLine({ tag: 'draft-1', state: 'unreadable', reason: 'not a bundle' })).toBe('draft-1 is unreadable: not a bundle');
    expect(draftLine({ tag: 'draft-1', state: 'unreadable' })).toBe('draft-1 is unreadable');
  });
});

describe('signUrl', () => {
  it('carries the ticket and comes back to the panel with the intent, never an accept list', () => {
    const u = signUrl('t/1', 'default/k8s:shop/Deployment/cart', 'sha256:abc');
    expect(u.startsWith('/auth/sign?ticket=t%2F1&return=')).toBe(true);
    const ret = new URLSearchParams(u.split('?')[1]).get('return');
    expect(ret).toBe('/#/fleet/services/default%2Fk8s%3Ashop%2FDeployment%2Fcart?declare=1&signed=sha256%3Aabc');
    expect(u).not.toContain('accept');
  });

  it('comes back from a signing login for an inferred golden path to the Golden paths list', () => {
    const key = 'template:default/springboot-service';
    expect(isTemplateKey(key)).toBe(true);
    expect(isTemplateKey('default/k8s:shop/Deployment/cart')).toBe(false);
    const want = '/#/fleet/golden-paths?declare=template%3Adefault%2Fspringboot-service&signed=sha256%3Aabc';
    for (const u of [signUrl('t', key, 'sha256:abc'), acceptSignUrl('t', key, 'sha256:abc')]) {
      expect(new URLSearchParams(u.split('?')[1]).get('return')).toBe(want);
    }
    expect(new URLSearchParams(acceptSignUrl('t', 'default/cart', 'sha256:abc').split('?')[1]).get('return')).toBe('/#/fleet/services/default%2Fcart?signed=sha256%3Aabc');
  });
});

describe('declareTarget', () => {
  it('names an inferred service for a declare action only', () => {
    const inferred = { kind: 'service', key: 'k', declaration: 'inferred' };
    expect(declareTarget({ verb: 'declare' }, inferred)).toBe('k');
    expect(declareTarget({ verb: 'edit-contract' }, inferred)).toBe('');
    expect(declareTarget({ verb: 'declare' }, { kind: 'service', key: 'k', declaration: 'declared' })).toBe('');
    expect(declareTarget({ verb: 'declare' }, null)).toBe('');
    expect(declareTarget(null, inferred)).toBe('');
    expect(declareTarget({ verb: 'declare' }, { kind: 'service', declaration: 'inferred' })).toBe('');
  });
});

describe('moved', () => {
  it('names what differs between two plans', () => {
    const a = { ...plan, owner: 'ana', signer: { subject: 'x' }, contract: { text: 'a' } };
    expect(moved(a, { ...a, version: '0.2.0' })).toBe('the version (0.1.0 to 0.2.0)');
    expect(moved(a, { ...a, deploy: 'img', owner: 'bob', signer: { subject: 'y' }, ranges: [], contract: { text: 'b' } }))
      .toBe('the deploy artifact, the owner, the signer, the ranges, the files');
    expect(moved(a, a)).toBe('what the cluster read');
    expect(moved({}, { version: '1' })).toBe('the version (none to 1)');
    expect(moved(null, a)).toBe('the plan');
  });
});

describe('claims', () => {
  it('signReturn is the page signUrl comes back to', () => {
    for (const key of ['default/k8s:shop/Deployment/cart', 'external:stripe']) {
      expect(new URLSearchParams(signUrl('t', key, 'sha256:abc').split('?')[1]).get('return')).toBe('/' + signReturn(key, 'sha256:abc'));
    }
    expect(signReturn('external:stripe', 'sha256:abc')).toBe('#/fleet/externals/stripe?declare=1&signed=sha256%3Aabc');
  });
  it('comes back from a signing login to the external system page', () => {
    const ret = new URLSearchParams(signUrl('t', 'external:aws/sqs/x', 'sha256:abc').split('?')[1]).get('return');
    expect(ret).toBe('/#/fleet/externals/aws%2Fsqs%2Fx?declare=1&signed=sha256%3Aabc');
  });
  it('names the external system of a claim subject only', () => {
    expect(claimId('external:stripe')).toBe('stripe');
    expect(claimId('default/k8s:shop/Deployment/cart')).toBe('');
  });
  it('requires an owner and checks the team and name patterns', () => {
    expect(claimFieldError('', '')).toEqual({ field: 'owner', message: 'Name the team that owns it' });
    expect(claimFieldError('team platform', '').message).toMatch(/^A team uses/);
    expect(claimFieldError('x'.repeat(254), '').field).toBe('owner');
    expect(claimFieldError('team-platform', 'Payments')).toEqual({ field: 'name', message: expect.stringMatching(/^A service name uses/) });
    expect(claimFieldError('team-platform', 'a'.repeat(64)).field).toBe('name');
    expect(claimFieldError('org/team-platform', 'payments-stripe')).toEqual({ field: '', message: '' });
    expect(claimFieldError('team-platform', '').message).toBe('');
    expect(claimFieldError('team-platform', '', 'Postgres SQL')).toEqual({ field: 'offering', message: expect.stringMatching(/^An offering uses/) });
    expect(claimFieldError('team-platform', '', 'a'.repeat(64)).field).toBe('offering');
    expect(claimFieldError('team-platform', '', 'postgresql')).toEqual({ field: '', message: '' });
  });
});

describe('offerings', () => {
  it('requires a release version and checks an owner only when one is given', () => {
    expect(offeringFieldError('', '')).toEqual({ field: 'version', message: 'Set the version the declared offering is published at' });
    expect(offeringFieldError('1.0', '').message).toMatch(/^A version is MAJOR.MINOR.PATCH/);
    expect(offeringFieldError('1.0.0-rc.1', '').field).toBe('version');
    expect(offeringFieldError('1.0.0', 'team storage')).toEqual({ field: 'owner', message: expect.stringMatching(/^A team uses/) });
    expect(offeringFieldError('1.0.0', 'x'.repeat(254)).field).toBe('owner');
    expect(offeringFieldError('1.0.0', '')).toEqual({ field: '', message: '' });
    expect(offeringFieldError('10.2.33', 'team-storage')).toEqual({ field: '', message: '' });
  });
  it('lists each open path of an OfferingSchemaOpen refusal and nothing for another one', () => {
    const message = "the offering's parameters are not a closed space, open at provisioning: schema.properties.tags, provisioning: schema.properties.class: close each path in a folder (additionalProperties: false, a type, a class enum) and accept it with --dir";
    expect(openPaths({ code: 'OfferingSchemaOpen', message })).toEqual(['provisioning: schema.properties.tags', 'provisioning: schema.properties.class']);
    expect(openPaths({ code: 'OfferingSchemaOpen', message: 'something else' })).toEqual([]);
    expect(openPaths({ code: 'VersionRequired', message })).toEqual([]);
    expect(openPaths(null)).toEqual([]);
  });
});
