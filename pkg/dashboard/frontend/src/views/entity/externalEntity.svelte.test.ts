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

  it('words the mark as unsigned otherwise', () => {
    const t = render(provider({ surface }));
    expect(t.querySelector('#sec-inferred-contract .visually-hidden')?.textContent).toBe('Unsigned, publisher unknown; a range is never checked against it');
  });

  it('lists the surface as ul lists with visible sources in a nested collapsed block', () => {
    const t = render(provider({ surface }));
    const nested = t.querySelector('#sec-inferred-contract details') as HTMLDetailsElement;
    expect(nested.open).toBe(false);
    const lists = nested.querySelectorAll('ul');
    expect(lists.length).toBe(3);
    const host = lists[0].querySelector('li')!;
    expect(host.textContent).toContain('api.stripe.com:443');
    expect(host.querySelector('.xe-sources')?.textContent).toContain('cluster-a, repo-b');
    expect(host.querySelector('.xe-sources')?.closest('[title]')).toBeNull();
  });

  it('says the surface is hidden when the view dropped it', () => {
    const t = render(provider({}));
    expect(t.querySelector('[data-testid="inferred-surface-hidden"]')?.textContent).toBe('Surface hidden: some sources are outside your view');
    expect(t.querySelector('#sec-inferred-contract details')).toBeNull();
  });
});
