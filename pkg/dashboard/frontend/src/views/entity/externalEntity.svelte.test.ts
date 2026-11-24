/**
 * The External page's consumers (517-S2): required and optional describe a
 * declaration, so a consumer no contract declares says how Pacto knows it.
 */
import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import ExternalEntity from './ExternalEntity.svelte';

let cleanup: (() => void) | null = null;
afterEach(() => { cleanup?.(); cleanup = null; });

const ref = (key: string) => ({ kind: 'service', key, label: key });

describe('ExternalEntity consumers', () => {
  it('labels declared consumers required or optional and the others by provenance', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const detail = { external: {
      external: { key: 'twilio', label: 'Twilio', class: 'vendor-api', consumers: ['a', 'b', 'c'], consumersTotal: 3 },
      consumers: [
        { service: ref('a'), provenance: 'declared', required: true, versions: ['2010-04-01'] },
        { service: ref('b'), provenance: 'observed' },
        { service: ref('c'), provenance: 'inferred' },
      ],
    } };
    const c = mount(ExternalEntity, { target, props: { detail } });
    flushSync();
    cleanup = () => { unmount(c); target.remove(); };
    const uses = [...target.querySelectorAll('[data-testid="external-consumer-use"]')].map((e) => e.textContent?.trim());
    expect(uses).toEqual(['required', 'observed only', 'inferred only']);
  });
});

describe('ExternalEntity inferred contract', () => {
  const longId = `host/${'very-long-legacy-segment-'.repeat(8)}example`;
  const value = (v: string, sources: string[]) => ({ value: v, sources });
  const provider = (rev: Record<string, unknown>) => ({
    repository: `registry.example/acme/pacto-inferred/${longId}`, rangeStatus: 'unknown', rangeReason: 'INFERRED_PROVIDER', revisionsTotal: 1,
    revisions: [{ version: '0.0.3', digest: 'sha256:abc', ...rev }],
  });
  const surface = {
    identity: longId, class: 'legacy', asOf: '2026-11-17T00:00:00Z', completeness: { state: 'complete' },
    hosts: [value('api.stripe.com:443', ['cluster-a', 'repo-b'])], ports: [value('443', ['cluster-a'])], clients: [value('stripe-go/76', ['repo-b'])],
  };
  const render = (prov: unknown) => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const detail = { external: { external: { key: longId, label: longId, class: 'legacy', consumers: [], consumersTotal: 0, ...(prov ? { provider: prov } : {}) }, consumers: [] } };
    const c = mount(ExternalEntity, { target, props: { detail } });
    flushSync();
    cleanup = () => { unmount(c); target.remove(); };
    return target;
  };

  it('is absent without a provider', () => {
    const t = render(null);
    expect(t.querySelector('#sec-inferred-contract')).toBeNull();
  });

  it('shows one collapsed block with the repository and version in bdi', () => {
    const t = render(provider({ surface, signature: { status: 'unverified', source: 'operator', bundles: 1 } }));
    const block = t.querySelector('details#sec-inferred-contract') as HTMLDetailsElement;
    expect(block).not.toBeNull();
    expect(block.open).toBe(false);
    expect(block.dataset.toc).toBe('Inferred contract');
    const bdis = [...block.querySelectorAll('bdi')].map((b) => b.textContent);
    expect(bdis).toContain(`registry.example/acme/pacto-inferred/${longId}`);
    expect(bdis).toContain('0.0.3');
    expect(block.textContent).toContain('Range checks: unverified');
  });

  it('words the mark from the operator signature as visually hidden text', () => {
    const t = render(provider({ surface, signature: { status: 'unverified', source: 'operator', bundles: 1 } }));
    const hidden = t.querySelector('#sec-inferred-contract .visually-hidden');
    expect(hidden?.textContent).toBe("Published and signed by this installation's operator from what Pacto sees; a range is never checked against it");
  });

  it('words the mark from what the signature check established', () => {
    const mark = (signature?: Record<string, unknown>) => {
      const t = render(provider({ surface, ...(signature ? { signature } : {}) }));
      const text = t.querySelector('#sec-inferred-contract .visually-hidden')?.textContent;
      cleanup?.(); cleanup = null;
      return text;
    };
    expect(mark()).toBe('Signature not checked, publisher unknown; a range is never checked against it');
    expect(mark({ status: 'unsigned', source: 'none' })).toBe('Unsigned, publisher unknown; a range is never checked against it');
    expect(mark({ status: 'unknown', source: 'none' })).toBe('Signature could not be checked, publisher unknown; a range is never checked against it');
    expect(mark({ status: 'verified', source: 'rule', signer: 'ci@acme' })).toBe("Signed by ci@acme, not by this installation's operator (verified); a range is never checked against it");
    expect(mark({ status: 'unverified', source: 'inferred' })).toBe("Signed, not by this installation's operator (unverified); a range is never checked against it");
  });

  it('lists the surface as ul lists with visible sources in a nested collapsed block', () => {
    const t = render(provider({ surface }));
    const nested = t.querySelector('#sec-inferred-contract details') as HTMLDetailsElement;
    expect(nested.open).toBe(false);
    expect(nested.querySelector('summary')?.textContent).toBe('What the record lists');
    const lists = nested.querySelectorAll('ul');
    expect(lists.length).toBe(3);
    const host = lists[0].querySelector('li')!;
    expect(host.textContent).toContain('api.stripe.com:443');
    expect(host.querySelector('.xe-sources')?.textContent).toContain('cluster-a, repo-b');
    expect(host.querySelector('.xe-sources')?.closest('[title]')).toBeNull();
  });

  it('says the surface is hidden only when the view is restricted', () => {
    const t = render({ ...provider({}), surfaceHidden: true });
    expect(t.querySelector('[data-testid="inferred-surface-hidden"]')?.textContent).toBe('Surface hidden: your view is restricted');
    expect(t.querySelector('#sec-inferred-contract details')).toBeNull();
  });

  it('says the revision records no surface in an unrestricted view', () => {
    const t = render(provider({}));
    expect(t.querySelector('[data-testid="inferred-surface-hidden"]')?.textContent).toBe('This revision records no surface');
  });

  it('says when the surface was computed, that a partial run carries values over and when each value was last seen', () => {
    const lastSeen = new Date(Date.now() - 3 * 3600_000).toISOString();
    const partial = { ...surface, asOf: new Date(Date.now() - 3600_000).toISOString(), completeness: { state: 'partial', limitations: ['EXTERNALS_UNREPORTED'] },
      hosts: [{ ...value('api-old.stripe.com:443', ['k8s']), lastSeen }] };
    const t = render(provider({ surface: partial, signature: { status: 'unverified', source: 'operator', bundles: 1 } }));
    const nested = t.querySelector('#sec-inferred-contract details') as HTMLDetailsElement;
    expect(nested.querySelector('summary')?.textContent).toBe("What this installation's operator saw");
    expect(t.querySelector('[data-testid="inferred-surface-meta"]')?.textContent).toBe(
      'Computed 1 hour ago; partial (EXTERNALS_UNREPORTED): some sources did not answer, so values may be carried over from earlier runs');
    expect(nested.querySelector('.xe-sources')?.textContent).toBe('seen by k8s, last seen 3 hours ago');
    cleanup?.(); cleanup = null;
    const t2 = render(provider({ surface }));
    expect(t2.querySelector('[data-testid="inferred-surface-meta"]')?.textContent).toMatch(/; complete$/);
  });

  const change = (o: Record<string, unknown> = {}) => ({
    from: '0.0.1', to: '0.0.2', at: new Date(Date.now() - 2 * 3600_000).toISOString(), checked: true,
    openapiFrom: `oci://registry.example/vendor/stripe-openapi@sha256:${'a'.repeat(64)}`, openapiTo: `oci://registry.example/vendor/stripe-openapi@sha256:${'b'.repeat(64)}`,
    added: ['clients stripe-go/77'], removed: ['clients stripe-go/76'], ...o,
  });
  it('caps the change values in the tip', () => {
    const many = Array.from({ length: 2000 }, (_, i) => `hosts h${i}.example:443`);
    const t = render({ ...provider({ surface }), change: change({ added: many, removed: null }) });
    const tip = t.querySelector('[data-testid="external-provider-change"] [role="note"]')?.textContent ?? '';
    expect(tip).toContain('hosts h9.example:443');
    expect(tip).not.toContain('hosts h10.example:443');
    expect(tip).toContain('and 1,990 more');
  });
  it('the inferred contract disclosure says what changed', () => {
    const t = render({ ...provider({ surface }), change: change() });
    const line = t.querySelector('#sec-inferred-contract [data-testid="external-provider-change"]') as HTMLElement;
    expect(line.querySelector('.xe-change-words')?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Changed 0.0.1 → 0.0.2 (2 hours ago)');
    expect(line.querySelector('button[aria-label="About this change"]')).toBeTruthy();
    const tip = line.querySelector('[role="note"]')?.textContent ?? '';
    expect(tip).toContain(`OpenAPI oci://registry.example/vendor/stripe-openapi@sha256:${'a'.repeat(64)} → oci://registry.example/vendor/stripe-openapi@sha256:${'b'.repeat(64)}`);
    expect(tip).toContain('added clients stripe-go/77');
    expect(tip).toContain('removed clients stripe-go/76');
  });

  it('an unchecked change says unchecked', () => {
    const t = render({ ...provider({ surface }), change: change({ checked: false, added: null, removed: null }) });
    const line = t.querySelector('[data-testid="external-provider-change"]') as HTMLElement;
    expect(line.querySelector('.xe-change-words')?.textContent?.replace(/\s+/g, ' ').trim()).toBe('Changed 0.0.1 → 0.0.2 (2 hours ago), unchecked');
    expect(line.querySelector('[role="note"]')?.textContent).toContain("No source checked these revisions' signatures");
  });

  it('no change line without a change', () => {
    const t = render(provider({ surface }));
    expect(t.querySelector('[data-testid="external-provider-change"]')).toBeNull();
  });

  it('caps each list at 12 with its total and a Show all button', () => {
    const hosts = Array.from({ length: 2000 }, (_, i) => value(`h${String(i).padStart(4, '0')}.s3.amazonaws.com:443`, ['k8s']));
    const t = render(provider({ surface: { ...surface, hosts } }));
    const list = t.querySelector('#sec-inferred-contract details ul') as HTMLUListElement;
    expect(list.querySelectorAll('li').length).toBe(12);
    expect(t.querySelector('#sec-inferred-contract details h3')?.textContent).toBe('Hosts (2,000)');
    const more = t.querySelector('[data-testid="inferred-surface-hosts-show-all"]') as HTMLButtonElement;
    expect(more.textContent?.trim()).toBe('Show all 2,000 hosts');
    more.click();
    flushSync();
    expect(list.querySelectorAll('li').length).toBe(2000);
    expect(t.querySelector('[data-testid="inferred-surface-ports-show-all"]')).toBeNull();
  });
});

describe('ExternalEntity owner', () => {
  const render = (rec: Record<string, unknown>, props: Record<string, unknown> = {}) => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const detail = { external: { external: { key: 'stripe', label: 'Stripe', class: 'vendor-api', consumers: [], consumersTotal: 2, ...rec }, consumers: [] } };
    const c = mount(ExternalEntity, { target, props: { detail, ...props } });
    flushSync();
    cleanup = () => { unmount(c); target.remove(); };
    return target;
  };
  const tip = (t: HTMLElement) => t.querySelector('[data-testid="external-owner"] [data-tip], [data-testid="external-owner"] [title]');

  it('says an unclaimed system is routed for routing only, with a Claim button only for a caller who can declare', () => {
    const t = render({});
    expect(t.querySelector('[data-testid="external-owner-unclaimed"]')?.textContent).toContain('Unclaimed');
    expect(t.querySelector('[data-testid="external-owner"]')?.innerHTML).toContain("Routed to the installation's default approvers (routing only; it grants no right)");
    expect(t.querySelector('[data-testid="external-claim"]')).toBeNull();
    cleanup?.();
    const w = render({}, { canDeclare: true });
    const btn = w.querySelector('[data-testid="external-claim"]') as HTMLButtonElement;
    expect(btn.getAttribute('aria-expanded')).toBe('false');
    btn.click();
    flushSync();
    expect(btn.getAttribute('aria-expanded')).toBe('true');
    expect(w.querySelector('[data-testid="claim-fields"]')).toBeTruthy();
  });

  it('names the owner and the claiming service, with who declared it in the mark', () => {
    const t = render({ claim: { service: 'payments-stripe', version: '0.2.0', declaredVersion: '0.1.0', owner: 'team-platform', ownerKey: 'team:team-platform', by: 'ana@acme.com', ref: 'ghcr.io/acme/payments-stripe@sha256:abc' } }, { canDeclare: true });
    const line = t.querySelector('[data-testid="external-owner-claimed"]');
    expect(line?.textContent?.replace(/\s+/g, ' ')).toContain('team-platform, claimed by payments-stripe 0.2.0');
    expect(line?.querySelector('a[href="#/owners/team%3Ateam-platform"]')).toBeTruthy();
    expect(line?.querySelector('a[href="#/fleet/services/payments-stripe"]')?.getAttribute('title')).toBe('payments-stripe');
    expect(tip(t as HTMLElement)).toBeTruthy();
    // The claim names the version it was established at, apart from the newest revision.
    expect(line?.innerHTML).toContain('Claim established by ana@acme.com at 0.1.0; newest revision ghcr.io/acme/payments-stripe@sha256:abc');
    // A claim again opens the panel with the claimed name.
    (t.querySelector('[data-testid="external-claim"]') as HTMLButtonElement).click();
    flushSync();
    expect((t.querySelector('[data-testid="claim-name"]') as HTMLInputElement).value).toBe('payments-stripe');
  });

  it('says when the claiming service has no verified owner', () => {
    const t = render({ claim: { service: 'payments-stripe', version: '0.2.0' } });
    expect(t.querySelector('[data-testid="external-owner-claimed"]')?.textContent).toContain('No verified owner');
    expect(t.querySelector('[data-testid="external-owner-claimed"]')?.innerHTML).toContain('Claim established by an unnamed person');
  });

  it('links a DRI-only owner by its dri key', () => {
    const t = render({ claim: { service: 'payments-stripe', version: '0.2.0', owner: 'alice', ownerKey: 'dri:alice' } });
    const line = t.querySelector('[data-testid="external-owner-claimed"]');
    expect(line?.querySelector('a[href="#/owners/dri%3Aalice"]')).toBeTruthy();
    expect(line?.querySelector('a[href="#/owners/team%3Aalice"]')).toBeNull();
  });

  it('says more than one claim counts as none, with no Claim button', () => {
    const t = render({ claimants: 2 }, { canDeclare: true });
    expect(t.querySelector('[data-testid="external-owner-claimants"]')?.textContent).toBe('Claimed by 2 services, so no claim counts until all but one are retracted');
    expect(t.querySelector('[data-testid="external-owner-unclaimed"]')).toBeNull();
    expect(t.querySelector('[data-testid="external-claim"]')).toBeNull();
  });

  it('shows a claim the caller cannot see with no Claim button', () => {
    const t = render({ claim: { hidden: true } }, { canDeclare: true });
    expect(t.querySelector('[data-testid="external-owner-hidden"]')?.textContent).toBe('Claimed by a service you cannot see');
    expect(t.querySelector('[data-testid="external-claim"]')).toBeNull();
  });

  it('shows no owner on the other-egress node', () => {
    const t = render({ key: 'other-egress', class: 'other-egress' }, { canDeclare: true });
    expect(t.querySelector('[data-testid="external-owner"]')).toBeNull();
  });

  it('opens the claim panel back from a signing login', () => {
    const t = render({}, { canDeclare: true, declareQuery: { open: true, signed: 'sha256:i1' } });
    expect(t.querySelector('[data-testid="claim-fields"]')).toBeTruthy();
  });
});

describe('ExternalEntity resources (517-S7)', () => {
  const ID = 'aws/rds/123456789012/eu-west-1/orders-db';
  const xp = (over: Record<string, unknown> = {}) => ({
    source: 'crossplane', type: 'rds.aws.upbound.io/Instance', name: 'orders-db', cluster: '3f9a1c2e-uid-prod', clusterName: 'prod',
    engine: 'postgres 16', provenance: 'inferred', contract: 'outside', observedAt: '2026-11-23T09:00:00Z', claim: 'PostgreSQLInstance shop/orders-db', ...over,
  });
  const tf = (over: Record<string, unknown> = {}) => ({
    source: 'terraform', type: 'aws_db_instance', name: 'aws_db_instance.orders', location: 's3://acme-tf/prod.tfstate', cluster: '3f9a1c2e-uid-prod', clusterName: 'prod',
    provenance: 'inferred', contract: 'offering', offering: { kind: 'offering', key: 'postgresql', label: 'postgresql' }, evidence: 'crossplane-adapter, 2026-11-23T08:00:00Z', ...over,
  });
  const render = (ext: Record<string, unknown>, props: Record<string, unknown> = {}) => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const detail = { external: { external: { key: ID, label: 'orders-db', class: 'managed-cloud', consumers: [], consumersTotal: 0, ...ext }, consumers: [] } };
    const c = mount(ExternalEntity, { target, props: { detail, ...props } });
    flushSync();
    cleanup = () => { unmount(c); target.remove(); };
    return target;
  };
  const cards = (t: HTMLElement) => [...t.querySelectorAll('[data-testid="external-resource"]')] as HTMLElement[];
  const words = (el: Element | null | undefined) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

  it('renders resources with contract state', () => {
    const t = render({ resources: [xp(), tf()], resourcesTotal: 2 });
    const sec = t.querySelector('#sec-resources') as HTMLElement;
    expect(sec.dataset.toc).toBe('Resources');
    // Between Consumers and the inferred contract, before Provenance.
    const order = [...t.querySelectorAll('section[id], details[id]')].map((e) => e.id);
    expect(order.indexOf('sec-resources')).toBe(order.indexOf('sec-consumers') + 1);
    // Grouped by the reader, one visual card per resource, no table.
    expect(sec.querySelector('table')).toBeNull();
    expect([...sec.querySelectorAll('[data-testid="external-resource-group"] h3')].map((h) => h.textContent)).toEqual(['Crossplane', 'Terraform']);
    const [obj, state] = cards(t);
    expect(words(obj)).toContain('orders-db');
    expect(words(obj)).toContain('postgres 16');
    expect(words(obj.querySelector('[data-testid="external-resource-contract"]'))).toBe('Outside contract');
    expect(obj.querySelector('[data-testid="external-resource-contract"]')?.classList.contains('t-meta')).toBe(true);
    expect(words(state)).toContain('aws_db_instance.orders');
    const through = state.querySelector('[data-testid="external-resource-contract"]')!;
    expect(words(through)).toBe('Through offering postgresql');
    expect(through.querySelector('a')?.getAttribute('href')).toBe('#/fleet/offerings/postgresql');
    // A discreet inferred mark carries the source, the location and when it was read.
    expect(state.innerHTML).toContain('Inferred from Terraform state s3://acme-tf/prod.tfstate');
    expect(obj.innerHTML).toContain('Inferred from a Crossplane object');
    expect(t.querySelector('[data-testid="external-resources-caption"]')?.textContent).toContain('2 managed resources');
    expect(t.querySelector('[data-testid="external-resources-caption"]')?.textContent).toContain('1 outside the contract');
  });

  it('outside resource offers both claim paths', () => {
    const t = render({ resources: [xp(), tf()], resourcesTotal: 2 }, { canDeclare: true });
    const [obj, state] = cards(t);
    expect(state.querySelector('[data-testid="resource-claim-join"]')).toBeNull();
    const join = obj.querySelector('[data-testid="resource-claim-join"]') as HTMLButtonElement;
    const propose = obj.querySelector('[data-testid="resource-claim-propose"]') as HTMLButtonElement;
    expect(join.textContent).toBe('Claim into an offering');
    expect(propose.textContent).toBe('Propose an offering');
    // They name the panel they control only once it exists (axe aria-valid-attr-value).
    expect(join.hasAttribute('aria-controls')).toBe(false);
    propose.click();
    flushSync();
    expect(join.getAttribute('aria-controls')).toBe('xe-claim-panel');
    // The existing claim panel opens, with its Offering field focused.
    const field = t.querySelector('[data-testid="claim-offering"]') as HTMLInputElement;
    expect(field).toBeTruthy();
    expect(document.activeElement).toBe(field);
    // Without the right to declare there are no actions.
    cleanup?.();
    const r = render({ resources: [xp()], resourcesTotal: 1 });
    expect(r.querySelector('[data-testid="resource-claim-join"]')).toBeNull();
  });

  it('resource label shows cluster display name not uid', () => {
    const t = render({ resources: [xp()], resourcesTotal: 1 });
    const [obj] = cards(t);
    expect(words(obj)).toContain('prod');
    expect(obj.textContent).not.toContain('3f9a1c2e-uid-prod');
    // A resource whose cluster has no display name names none.
    cleanup?.();
    const u = render({ resources: [xp({ clusterName: '' })], resourcesTotal: 1 });
    expect(u.textContent).not.toContain('3f9a1c2e-uid-prod');
  });

  it('hidden resources counted not named', () => {
    const many = Array.from({ length: 20 }, (_, i) => xp({ name: `db-${i}` }));
    const t = render({ resources: many, resourcesTotal: 26, hiddenResources: 3 });
    expect(cards(t)).toHaveLength(20);
    expect(words(t.querySelector('[data-testid="external-resources-more"]'))).toBe('and 6 more');
    expect(words(t.querySelector('[data-testid="external-resources-hidden"]'))).toBe('3 more resources you cannot see.');
    expect(t.querySelector('[data-testid="external-resources-hidden"]')?.querySelector('bdi')).toBeNull();
  });

  it('requested engine is muted', () => {
    const t = render({ resources: [xp({ engine: '', engineRequested: 'postgres 16' })], resourcesTotal: 1 });
    const eng = t.querySelector('[data-testid="external-resource-engine"]')!;
    expect(words(eng)).toBe('requested postgres 16');
    expect(eng.classList.contains('t-meta')).toBe(true);
  });

  it('is absent when no resource was read', () => {
    const t = render({});
    expect(t.querySelector('#sec-resources')).toBeNull();
  });
});
