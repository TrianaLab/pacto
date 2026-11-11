/**
 * Delivery on the revision and target pages (#466 B4): a revision's way through its
 * service's Stages, read from the lifecycle answer, and a target's link to the Stage
 * that delivers its environment. Unknown never reads as not reached, an unmatched
 * revision is a neutral state and an environment label matches only exactly.
 */
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, unmount, flushSync, tick } from 'svelte';

const fns = vi.hoisted(() => ({ lifecycle: vi.fn(), environments: vi.fn(), record: vi.fn() }));
vi.mock('../../lib/api.ts', async (importOriginal) => {
  const actual = await importOriginal<typeof import('../../lib/api.ts')>();
  return { ...actual, api: { ...actual.api,
    deliveryLifecycle: (...a: unknown[]) => fns.lifecycle(...a),
    deliveryEnvironments: (...a: unknown[]) => fns.environments(...a),
    deliveryRecord: (...a: unknown[]) => fns.record(...a),
  } };
});

import { ApiError } from '../../lib/api.ts';
// @ts-expect-error — Svelte component has no declaration file
import RevisionEntity from './RevisionEntity.svelte';
// @ts-expect-error — Svelte component has no declaration file
import TargetEntity from './TargetEntity.svelte';
// @ts-expect-error — Svelte component has no declaration file
import VerdictList from '../../components/VerdictList.svelte';

const DIG = 'sha256:' + 'c'.repeat(64);
const ART = 'sha256:' + 'a'.repeat(64);
const REC = 'sha256:' + 'e'.repeat(64);

async function settle() {
  for (let i = 0; i < 6; i++) { await tick(); await Promise.resolve(); }
  flushSync();
}
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- component handle
function render(C: any, props: Record<string, unknown>) {
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(C, { target, props });
  return { target, destroy: () => { unmount(c); target.remove(); } };
}
const text = (el: Element | null) => el?.textContent?.replace(/\s+/g, ' ').trim() ?? '';
const preview = (items: unknown[] = []) => ({ total: items.length, count: items.length, truncated: false, items });

function revision() {
  return { entity: { kind: 'revision', key: 'story/payments@' + DIG }, revision: {
    service: { kind: 'service', key: 'story/payments', label: 'payments', domain: 'registry.example/story', href: '#' }, valid: true,
    identity: { digest: DIG }, provenance: {}, validation: preview(), interfaces: preview(), configurations: preview(), policies: preview(),
    capabilities: preview(), dependencies: preview(), tools: preview(), skills: preview(), docs: preview(), metadata: preview(),
    exactTargets: preview(), inferredTargets: preview(), environments: preview(), stages: [],
  } };
}
const ev = (role: string, over = {}) => ({ role, operation: 'payments-promote-2', type: 'delivery.pacto.trianalab.io/promote', actor: 'a', applied: '2026-11-10T09:00:00Z', ...over });
const stage = (name: string, over = {}) => ({ namespace: name, stage: 'payments', environment: name, running: false, verified: 'no', failed: 'no', offered: false,
  source: 'records', recordsVerified: true, completeness: 'complete', events: [], ...over });
const lifecycle = (stages: unknown[], over = {}) => ({ asOf: '2026-11-11T10:00:00Z', completeness: 'complete', service: 'payments', digest: ART, version: '2.0.0',
  signature: 'signed', signedOn: ['test/payments'], stages, note: '', ...over });

describe('revision page Delivery lifecycle (B4)', () => {
  beforeEach(() => { fns.lifecycle.mockReset(); fns.record.mockReset(); });

  it('asks by the revision digest and lists the published row, arrived, left, re-checked and not reached', async () => {
    fns.lifecycle.mockResolvedValue(lifecycle([
      stage('test', { running: false, verified: 'yes', events: [ev('arrived', { record: REC, verification: 'Verified' }), ev('left', { type: 'delivery.pacto.trianalab.io/rollback', applied: '', finished: '2026-11-10T11:00:00Z' })] }),
      stage('prod', { running: true, verified: 'unknown', completeness: 'partial', events: [ev('arrived')], lastCheck: { operation: 'u', digest: ART, at: '2026-11-11T09:58:00Z', verdicts: [{ gate: 'mira.health', phase: 'verify', result: 'Unknown' }] } }),
      stage('dev'),
    ]));
    const { target, destroy } = render(RevisionEntity, { detail: revision(), deliveryState: 'enabled' });
    await settle();
    expect(fns.lifecycle).toHaveBeenCalledWith({ service: 'payments', domain: 'registry.example/story', contract: DIG });
    const sec = target.querySelector('[data-testid="revision-delivery"]') as HTMLElement;
    expect(text(sec)).toContain('Signature checked');
    expect(text(sec)).toContain('on test/payments');
    const rows = [...sec.querySelectorAll('.lc-stage')].map(text);
    expect(rows[0]).toContain('arrived: Promote');
    expect(rows[0]).toContain('verified');
    expect(rows[0]).toContain('moved off it: Roll back');
    expect(rows[0]).toContain('from status, not signed');
    expect(rows[1]).toContain('verified: not known');
    expect(rows[1]).toContain('Re-checked');
    expect(rows[1]).toContain('Unknown');
    expect(rows[2]).toContain('not reached');
    // A record id opens the record panel.
    fns.record.mockResolvedValue({ namespace: 'test', stage: 'payments', record: REC, verified: true, asOf: '', entry: ev('arrived'), verdicts: [], note: 'verified' });
    (sec.querySelector('button[aria-label^="Read record"]') as HTMLButtonElement).click();
    await settle();
    expect(fns.record).toHaveBeenCalledWith('test', 'payments', REC);
    expect(sec.querySelector('[data-testid="record-panel"]')).not.toBeNull();
    destroy();
  });

  it('never says not reached on a Stage whose history was read in part', async () => {
    fns.lifecycle.mockResolvedValue(lifecycle([stage('prod', { completeness: 'partial' })], { completeness: 'partial' }));
    const { target, destroy } = render(RevisionEntity, { detail: revision(), deliveryState: 'enabled' });
    await settle();
    const sec = text(target.querySelector('[data-testid="revision-delivery"]'));
    expect(sec).toContain('not seen in the history read (partial)');
    expect(sec).not.toContain('not reached');
    destroy();
  });

  it('words each signature as the Publish page does', async () => {
    for (const [sig, word] of [['signed', 'Signature checked'], ['unsigned', 'Not signed'], ['unknown', 'Signature not known here']]) {
      fns.lifecycle.mockResolvedValue(lifecycle([], { signature: sig, signedOn: [], unsignedOn: sig === 'unsigned' ? ['dev/payments'] : [] }));
      const { target, destroy } = render(RevisionEntity, { detail: revision(), deliveryState: 'enabled' });
      await settle();
      expect(text(target.querySelector('[data-testid="revision-delivery"]'))).toContain(word);
      destroy();
    }
  });

  it('reads a revision no Stage names as a neutral state, never an error', async () => {
    fns.lifecycle.mockRejectedValue(new ApiError(400, 'no artifact of payments that names contract sha256:c is known to its Stages', 'LifecycleUnresolved'));
    const { target, destroy } = render(RevisionEntity, { detail: revision(), deliveryState: 'enabled' });
    await settle();
    const sec = target.querySelector('[data-testid="revision-delivery"]') as HTMLElement;
    expect(text(sec)).toContain('No Stage of this service you can see names this revision');
    expect(sec.querySelector('[role="alert"]')).toBeNull();
    expect(text(sec)).not.toContain('not delivered');
    destroy();
  });

  it('renders nothing when delivery is not served', async () => {
    const { target, destroy } = render(RevisionEntity, { detail: revision(), deliveryState: 'not-served' });
    await settle();
    expect(fns.lifecycle).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="revision-delivery"]')).toBeNull();
    destroy();
  });
});

function targetDetail(env: string) {
  return { entity: { kind: 'target', key: 'k8s/prod/audit-log' }, target: {
    service: { kind: 'service', key: 'audit-log', label: 'audit-log', href: '#' }, linkState: 'exact', identity: {},
    environment: { name: env, declared: 'declared' }, findings: preview(), relationships: preview(),
  } };
}

describe('target page Delivery link (B4)', () => {
  beforeEach(() => { fns.environments.mockReset(); });
  const row = (environment: string) => ({ namespace: 'prod', stage: 'audit-log', service: 'audit-log', environment, state: 'verified' });

  it('links to the Stage whose environment equals the label', async () => {
    fns.environments.mockResolvedValue({ asOf: '', completeness: 'complete', environments: [row('prod')] });
    const { target, destroy } = render(TargetEntity, { detail: targetDetail('prod'), deliveryState: 'enabled' });
    await settle();
    expect(fns.environments).toHaveBeenCalledWith(expect.objectContaining({ service: 'audit-log', domain: '', environment: 'prod' }));
    const a = [...target.querySelectorAll('a')].find((x) => x.textContent === 'Delivery in prod');
    expect(a?.getAttribute('href')).toBe('#/delivery/history?namespace=prod&stage=audit-log');
    destroy();
  });

  it('never matches production against a prod Stage, and says the placement is observed', async () => {
    fns.environments.mockResolvedValue({ asOf: '', completeness: 'complete', environments: [row('prod')] });
    const { target, destroy } = render(TargetEntity, { detail: targetDetail('production'), deliveryState: 'enabled' });
    await settle();
    expect([...target.querySelectorAll('a')].some((x) => /^Delivery in/.test(x.textContent ?? ''))).toBe(false);
    expect(text(target.querySelector('[data-testid="target-delivery"]'))).toContain('observed');
    destroy();
  });

  it('says a partial listing may hide the Stage', async () => {
    fns.environments.mockResolvedValue({ asOf: '', completeness: 'partial', unreadable: ['prod'], environments: [] });
    const { target, destroy } = render(TargetEntity, { detail: targetDetail('prod'), deliveryState: 'enabled' });
    await settle();
    expect(text(target.querySelector('[data-testid="target-delivery"]'))).toContain('could not list every Stage');
    destroy();
  });

  it('asks nothing without delivery', async () => {
    const { target, destroy } = render(TargetEntity, { detail: targetDetail('prod'), deliveryState: 'not-served' });
    await settle();
    expect(fns.environments).not.toHaveBeenCalled();
    expect(target.querySelector('[data-testid="target-delivery"]')).toBeNull();
    destroy();
  });
});

describe('VerdictList contract citations (B4)', () => {
  it('lists each judged Pacto with its status and findings, linked to its target', () => {
    const key = 'kubernetes:prod/payments';
    const { target, destroy } = render(VerdictList, { verdicts: [
      { gate: 'pacto.contract', phase: 'verify', result: 'Fail', citation: { contract: 'oci://r/p@' + DIG, 'payments.target': key, 'payments.contractStatus': 'NonCompliant', 'payments.evaluatedAt': '2026-11-11T09:00:00Z', 'payments.findings': 'E101, W204' } },
      { gate: 'pacto.diff', phase: 'pre', result: 'Pass' },
    ] });
    const list = target.querySelector('[data-testid="contract-pactos"]') as HTMLElement;
    const a = list.querySelector('a') as HTMLAnchorElement;
    expect(a.textContent).toBe('payments');
    expect(a.getAttribute('href')).toBe('#/fleet/targets/' + encodeURIComponent(key));
    expect(text(list)).toContain('NonCompliant');
    expect(text(list)).toContain('E101, W204');
    expect([...target.querySelectorAll('[data-testid="verdict-badge"]')].map(text)).toEqual(['Fail', 'Checked']);
    destroy();
  });
});
