/**
 * Regression test: DocModal is an accessible dialog — it moves focus into itself on
 * open and traps Tab within, instead of leaving focus on the page behind it.
 */
import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { mount, unmount } from 'svelte';

// @ts-expect-error — Svelte component has no declaration file
import DocModal from './DocModal.svelte';

const flush = () => new Promise(resolve => setTimeout(resolve, 0));

describe('DocModal — focus management', () => {
  let target: HTMLElement;
  let trigger: HTMLButtonElement;

  beforeEach(() => {
    trigger = document.createElement('button');
    document.body.appendChild(trigger);
    trigger.focus();
    target = document.createElement('div');
    document.body.appendChild(target);
  });

  afterEach(() => {
    document.body.removeChild(target);
    document.body.removeChild(trigger);
  });

  it('moves focus into the dialog on open', async () => {
    const component = mount(DocModal, {
      target,
      props: { doc: { title: 'Runbook', path: 'docs/run.md', content: '# hi' }, onClose: () => {} },
    });
    await flush();

    const modal = target.querySelector('.doc-modal') as HTMLElement;
    expect(modal).toBeTruthy();
    expect(modal === document.activeElement || modal.contains(document.activeElement)).toBe(true);
    expect(document.activeElement).not.toBe(trigger);

    unmount(component);
  });

  it('traps Tab within the dialog', async () => {
    const component = mount(DocModal, {
      target,
      props: { doc: { title: 'Runbook', path: 'docs/run.md', content: '# hi' }, onClose: () => {} },
    });
    await flush();

    const modal = target.querySelector('.doc-modal') as HTMLElement;
    // Tab from anywhere in the dialog keeps focus inside it.
    window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true }));
    await flush();
    expect(modal === document.activeElement || modal.contains(document.activeElement)).toBe(true);

    unmount(component);
  });
});
