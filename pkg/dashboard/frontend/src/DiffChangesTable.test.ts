import { describe, it, expect, afterEach } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import DiffChangesTable from './DiffChangesTable.svelte';

let c: ReturnType<typeof mount> | null = null;
afterEach(() => { if (c) unmount(c); c = null; document.body.innerHTML = ''; });
const render = (changes: unknown[]) => { c = mount(DiffChangesTable, { target: document.body, props: { changes } }); flushSync(); return document.body; };
const ch = (i: number, reason = '') => ({ path: `service.f${i}`, type: 'modified', oldValue: 'a', newValue: 'b', classification: 'BREAKING', reason });

describe('DiffChangesTable', () => {
  it('hides a reason that only repeats the path and the change', () => {
    const b = render([ch(1, 'service.f1 modified'), ch(2, 'the port moved')]);
    expect(b.querySelectorAll('.diff-reason').length).toBe(1);
    expect(b.querySelector('.diff-reason')?.textContent).toBe('the port moved');
  });

  it('labels each cell for the stacked phone layout', () => {
    const b = render([ch(1)]);
    expect([...b.querySelectorAll('tbody td')].map((td) => td.getAttribute('data-label'))).toEqual(['Path', 'Change', 'Old', 'New', 'Breaking']);
  });

  it('shows fifty changes and offers the rest', () => {
    const b = render(Array.from({ length: 120 }, (_, i) => ch(i)));
    expect(b.querySelectorAll('tbody tr').length).toBe(50);
    (b.querySelector('[data-testid="diff-show-all"]') as HTMLButtonElement).click();
    flushSync();
    expect(b.querySelectorAll('tbody tr').length).toBe(120);
  });
});
