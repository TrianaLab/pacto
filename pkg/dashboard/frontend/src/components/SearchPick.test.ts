import { describe, it, expect, afterEach, vi } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import SearchPick from './SearchPick.svelte';

let c: ReturnType<typeof mount> | null = null;
afterEach(() => { if (c) unmount(c); c = null; document.body.innerHTML = ''; });
const opts = (n: number) => Array.from({ length: n }, (_, i) => ({ value: `k${i}`, label: `svc-${i}` }));

describe('SearchPick', () => {
  it('is a plain select for a short list', () => {
    const onpick = vi.fn();
    c = mount(SearchPick, { target: document.body, props: { id: 'p', label: 'Pick', options: opts(3), value: 'k1', onpick } });
    flushSync();
    const sel = document.querySelector('select') as HTMLSelectElement;
    expect(sel.value).toBe('k1');
    sel.value = 'k2';
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    expect(onpick).toHaveBeenCalledWith('k2');
  });

  it('is a searchable field for a long list, and picks only a whole option', () => {
    const onpick = vi.fn();
    c = mount(SearchPick, { target: document.body, props: { id: 'p', label: 'Pick', options: opts(400), value: '', onpick } });
    flushSync();
    expect(document.querySelectorAll('datalist option').length).toBe(400);
    const input = document.querySelector('input') as HTMLInputElement;
    input.value = 'svc-3';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    expect(onpick).toHaveBeenLastCalledWith('k3');
    input.value = 'svc-25';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(onpick).toHaveBeenLastCalledWith('k25'); // svc-25 is whole; svc-250..259 also contain it
    input.value = 'svc-39';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(onpick).toHaveBeenLastCalledWith('k39');
    input.value = 'svc-';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true }));
    expect(onpick).toHaveBeenCalledTimes(3);
  });

  it('clears the pick when the field is emptied', () => {
    const onpick = vi.fn();
    c = mount(SearchPick, { target: document.body, props: { id: 'p', label: 'Pick', options: opts(400), value: 'k5', onpick } });
    flushSync();
    const input = document.querySelector('input') as HTMLInputElement;
    expect(input.value).toBe('svc-5');
    input.value = '';
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    expect(onpick).toHaveBeenCalledWith('');
  });
});
