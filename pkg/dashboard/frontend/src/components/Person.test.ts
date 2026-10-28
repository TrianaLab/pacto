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
  it('names a person and keeps the opaque subject to the tooltip and the copy button', () => {
    const user = 'https://dex.example/dex#Cgd1c2VyLTAxEgVsb2NhbA';
    expect(text({ user, people: { [user]: 'Ann' } })?.replace(/\s+/g, ' ')).toBe('Ann ⧉');
    expect(document.body.querySelector('.copyable')?.getAttribute('title')).toBe(user);
    expect(document.body.querySelector('button')?.getAttribute('aria-label')).toBe('Copy username');
  });
  it('prints a plain username once', () => {
    expect(text({ user: 'kind-admin' })).toBe('kind-admin');
  });
});

describe('Person wrapping', () => {
  it('wraps an email after @ and dots, never inside a word', () => {
    expect(text({ user: 'carol@example.com' })).toBe('carol@example.com');
    expect(document.body.innerHTML).toContain('carol@<wbr>example.<wbr>com');
  });
});
