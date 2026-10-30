/**
 * Render tests for the behavior components (pactoVersion 2.2 and 2.3): interface
 * lifecycle, guarantees, refused and unverified requirements and the environments check.
 * Each verdict is the backend's; these assert it is named in words (never by color
 * alone), that Refused and Not verified never read alike, and that long values and
 * thousands of rows stay bounded on screen.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import LifecycleBadge from './LifecycleBadge.svelte';
// @ts-expect-error
import GuaranteeTerms from './GuaranteeTerms.svelte';
// @ts-expect-error
import BehaviorBadges from './BehaviorBadges.svelte';
// @ts-expect-error
import RequirementList from './RequirementList.svelte';
// @ts-expect-error
import EnvironmentList from './EnvironmentList.svelte';
// @ts-expect-error
import RelationshipList from './RelationshipList.svelte';

let target: HTMLElement;
let comp: ReturnType<typeof mount> | null = null;
beforeEach(() => { target = document.createElement('div'); document.body.appendChild(target); });
afterEach(() => { if (comp) { unmount(comp); comp = null; } document.body.removeChild(target); });

const tags = () => [...target.querySelectorAll('.tag')].map((t) => t.textContent);
const long = 'x'.repeat(400);

describe('LifecycleBadge', () => {
  const l = { deprecated: '2026-01-01', sunset: '2026-06-01', successor: 'orders-v2' };
  it('says deprecated before the sunset and past sunset after it', () => {
    comp = mount(LifecycleBadge, { target, props: { lifecycle: l, today: '2026-03-01' } });
    expect(tags()).toEqual(['Deprecated']);
    expect(target.querySelector('.tone-warn')).not.toBeNull();
    expect(target.textContent).toContain('Deprecated Jan 1, 2026 · sunset Jun 1, 2026 · successor orders-v2');
    unmount(comp);
    comp = mount(LifecycleBadge, { target, props: { lifecycle: l, today: '2026-06-01', detail: false } });
    expect(tags()).toEqual(['Past sunset']);
    expect(target.querySelector('.tone-err')?.getAttribute('title')).toMatch(/may be removed/);
    expect(target.querySelector('.lc-text')).toBeNull();
  });
  it('renders nothing for an interface that is not deprecated', () => {
    comp = mount(LifecycleBadge, { target, props: { lifecycle: null } });
    expect(target.textContent?.trim()).toBe('');
  });
});

describe('GuaranteeTerms', () => {
  it('pairs each label with its value', () => {
    comp = mount(GuaranteeTerms, { target, props: { guarantees: { delivery: 'at-least-once', timeout: '1s' }, required: true, label: 'Required from orders' } });
    const dl = target.querySelector('dl')!;
    expect(dl.getAttribute('aria-label')).toBe('Required from orders');
    expect([...dl.querySelectorAll('dt')].map((d) => d.textContent)).toEqual(['Delivery', 'Timeout']);
    expect([...dl.querySelectorAll('dd')].map((d) => d.textContent)).toEqual(['at-least-once', 'at most 1s']);
  });
  it('renders nothing when nothing is promised', () => {
    comp = mount(GuaranteeTerms, { target, props: { guarantees: {} } });
    expect(target.querySelector('dl')).toBeNull();
  });
});

describe('BehaviorBadges', () => {
  it('names the guarantee status and each deprecated interface', () => {
    comp = mount(BehaviorBadges, { target, props: { status: 'unmet', deprecated: [{ interface: 'orders', deprecated: '2026-01-01', sunset: '2026-02-01' }], today: '2026-03-01' } });
    expect(tags()).toEqual(['Refused', 'orders: past sunset']);
  });
  it('keeps Not verified neutral and carries its reason on hover', () => {
    comp = mount(BehaviorBadges, { target, props: { status: 'unknown', reason: 'the provider is hidden' } });
    const t = target.querySelector('.tag')!;
    expect(t.textContent).toBe('Not verified');
    expect(t.classList.contains('tone-neutral')).toBe(true);
    expect(t.getAttribute('title')).toBe('Not verified: the provider is hidden.');
  });
  it('caps a provider that deprecates hundreds of interfaces', () => {
    const deprecated = Array.from({ length: 500 }, (_, i) => ({ interface: `iface-${i}-${long}`, deprecated: '2026-01-01' }));
    comp = mount(BehaviorBadges, { target, props: { deprecated, today: '2026-01-02' } });
    expect(target.querySelectorAll('.tag').length).toBe(2);
    const more = target.querySelector('button.cl-more') as HTMLButtonElement;
    expect(more.textContent).toBe('Show all 500');
    expect(target.querySelector('.tag')!.getAttribute('title')).toContain(`iface-0-${long}`);
  });
});

describe('RequirementList', () => {
  it('lists what is required and what is refused', () => {
    comp = mount(RequirementList, { target, props: {
      requires: [{ interface: 'orders', delivery: 'at-least-once' }],
      unmet: [{ consumer: 'c', provider: 'p', interface: 'orders', guarantee: 'delivery', required: 'at-least-once', offered: 'at-most-once' }],
      status: 'unmet',
    } });
    expect(target.textContent).toContain('Requires from orders');
    expect(target.textContent).toContain('Refused');
    expect(target.textContent).toContain('orders: Delivery at-least-once required, at-most-once offered');
    expect(target.textContent).not.toContain('Not verified');
  });
  it('says why a check could not run and lists nothing as refused', () => {
    comp = mount(RequirementList, { target, props: { requires: [{ interface: 'orders', idempotent: true }], status: 'unknown', reason: 'no contract of the provider in the fleet to check against' } });
    expect(target.textContent).toContain('Not verified: no contract of the provider in the fleet to check against.');
    expect(target.querySelector('.rq-refused')).toBeNull();
  });
  it('caps a long refusal list', () => {
    const unmet = Array.from({ length: 1000 }, (_, i) => ({ consumer: 'c', provider: 'p', interface: `i${i}`, guarantee: 'timeout', required: '1s', offered: '5s' }));
    comp = mount(RequirementList, { target, props: { unmet } });
    expect(target.querySelectorAll('.rq-m').length).toBe(3);
    expect(target.querySelector('button.cl-more')?.textContent).toBe('Show all 1,000');
  });
});

describe('EnvironmentList', () => {
  const env = (name: string, status: string, extra = {}) => ({ name, file: `envs/${name}.yaml`, status, findings: { total: 0, count: 0, truncated: false, items: [] }, ...extra });
  it('names every status apart and locates a failure in its file', () => {
    comp = mount(EnvironmentList, { target, props: { items: [
      env('staging', 'pass'),
      env('production', 'fail', { findings: { total: 1, count: 1, truncated: false, items: [{ code: 'ENVIRONMENT_VALUES_INVALID', severity: 'error', path: 'environments[1].file', message: 'environment production: configuration app at /app/replicas: got string, want integer', file: 'envs/production.yaml', line: 2, column: 13 }] } }),
      env('dev', 'unverified', { findings: { total: 1, count: 1, truncated: false, items: [{ code: 'ENVIRONMENT_VALUES_UNVERIFIED', severity: 'warning', path: 'environments[2].file', message: 'cannot check' }] } }),
      env('qa', 'skip', { reason: 'policy failed' }),
    ] } });
    expect([...target.querySelectorAll('.env-head .tag')].map((t) => t.textContent)).toEqual(['Passed', 'Failed', 'Not verified', 'Not checked']);
    expect(target.textContent).toContain('envs/production.yaml:2:13');
    // The card names the environment, so its messages do not repeat it.
    expect(target.textContent).toContain('configuration app at /app/replicas: got string, want integer');
    expect(target.textContent).not.toContain('environment production: configuration');
    expect(target.querySelector('.env-msg')?.textContent).toBe('configuration app at /app/replicas: got string, want integer');
    expect(target.textContent).toContain('ENVIRONMENT_VALUES_INVALID');
    expect(target.textContent).toContain('Not checked: policy failed.');
  });
  it('stays bounded with long names, long paths and hundreds of findings', () => {
    const items = Array.from({ length: 200 }, (_, i) => env(`env-${i}-${long}`, 'fail', {
      file: `envs/${long}/${i}.yaml`,
      findings: { total: 300, count: 200, truncated: true, items: Array.from({ length: 200 }, (_, j) => ({ code: 'ENVIRONMENT_VALUES_INVALID', severity: 'error', path: 'p', message: `${j} ${long}`, file: 'f', line: j + 1 })) },
    }));
    comp = mount(EnvironmentList, { target, props: { items } });
    const first = target.querySelector('[data-testid="environment"]')!;
    expect(first.querySelectorAll('.env-f').length).toBe(5);
    expect(first.querySelector('button.cl-more')?.textContent).toBe('Show all 200');
    expect(first.textContent).toContain('Showing 200 of 300 findings.');
    expect(first.querySelector('.env-name')?.getAttribute('title')).toBe(`env-0-${long}`);
    (first.querySelector('button.cl-more') as HTMLButtonElement).click();
    flushSync();
    expect(first.querySelectorAll('.env-f').length).toBe(200);
  });
});

describe('RelationshipList behavior', () => {
  const edge = (over = {}) => ({
    id: 'a|b', relation: 'dependency',
    from: { kind: 'service', key: 'd/checkout', label: 'checkout' },
    to: { kind: 'service', key: 'd/events', label: 'events' },
    expected: true, observed: false, provenance: 'declared',
    declaredClaims: { total: 1, count: 1, truncated: false, items: [{ required: true, guaranteeStatus: 'unmet', requires: [{ interface: 'orders', delivery: 'at-least-once' }], unmet: [{ consumer: 'checkout', provider: 'events', interface: 'orders', guarantee: 'delivery', required: 'at-least-once', offered: 'at-most-once' }] }] },
    guaranteeStatus: 'unmet',
    deprecatedInterfaces: [{ interface: 'orders', deprecated: '2026-01-01' }],
    ...over,
  });
  it('shows the refusal and the deprecation on the consumer, with the declared detail on the inspector', () => {
    comp = mount(RelationshipList, { target, props: { items: [edge()], showClaims: true } });
    expect(target.textContent).toContain('Refused');
    expect(target.textContent).toContain('orders: deprecated');
    expect(target.textContent).toContain('Requires from orders');
    expect(target.textContent).toContain('orders: Delivery at-least-once required, at-most-once offered');
  });
  it('surfaces the consumer of a deprecated interface on the provider page', () => {
    comp = mount(RelationshipList, { target, props: { items: [edge({ guaranteeStatus: undefined })], selfKey: 'd/events' } });
    const row = target.querySelector('.rel')!;
    expect(row.textContent).toContain('Used by');
    expect(row.textContent).toContain('checkout');
    expect(row.textContent).toContain('orders: deprecated');
    // An inbound row is somebody else's declaration, so its claim detail stays off.
    expect(target.textContent).not.toContain('Requires from');
  });
  it('reads the edge reason from the claim that could not be checked', () => {
    comp = mount(RelationshipList, { target, props: { items: [edge({ guaranteeStatus: 'unknown', deprecatedInterfaces: [], declaredClaims: { total: 1, count: 1, truncated: false, items: [{ guaranteeStatus: 'unknown', guaranteeReason: 'hidden', requires: [{ interface: 'orders', idempotent: true }] }] } })], showClaims: true } });
    const badge = [...target.querySelectorAll('.tag')].find((t) => t.textContent === 'Not verified')!;
    expect(badge.getAttribute('title')).toBe('Not verified: hidden.');
    expect(target.textContent).not.toContain('Refused');
  });
});
