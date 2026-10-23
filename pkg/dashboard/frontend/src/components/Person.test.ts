import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte components have no declaration files
import Person from './Person.svelte';

let app: ReturnType<typeof mount> | null = null;
afterEach(() => { if (app) unmount(app); app = null; document.body.innerHTML = ''; });

const text = (props: Record<string, unknown>) => {
  app = mount(Person, { target: document.body, props });
  return document.body.textContent?.trim();
};

describe('Person', () => {
  it('prints the verifiable identity next to a self-asserted name, not in a tooltip', () => {
    expect(text({ user: 'https://idp#u1', people: { 'https://idp#u1': 'Ann' } })).toBe('Ann (https://idp#u1)');
    expect(document.body.querySelector('[title]')).toBeNull();
  });
  it('prints a plain username once', () => {
    expect(text({ user: 'kind-admin' })).toBe('kind-admin');
  });
});
