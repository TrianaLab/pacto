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
