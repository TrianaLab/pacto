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
