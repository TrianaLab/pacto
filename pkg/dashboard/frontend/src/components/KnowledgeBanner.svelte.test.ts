/**
 * A fleet read from a namespace list is partial on purpose: the banner says which
 * namespaces Pacto watches before it says why the snapshot is short.
 */
import { describe, it, expect } from 'vitest';
import { mount, unmount, flushSync } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import KnowledgeBanner from './KnowledgeBanner.svelte';
import { snapshotKnowledge } from '../lib/knowledgeState.ts';

const text = (el: Element | null) => (el?.textContent ?? '').replace(/\s+/g, ' ').trim();

function render(namespaces: string[], extra: Record<string, unknown> = {}) {
  const sources = [{ id: 'k8s', kind: 'kubernetes', status: 'partial', namespaces, ...extra }];
  const target = document.createElement('div');
  document.body.appendChild(target);
  const c = mount(KnowledgeBanner, { target, props: { knowledge: snapshotKnowledge({ completeness: 'partial', sources }), noun: 'overview', sources } });
  flushSync();
  return { target, done: () => { unmount(c); target.remove(); } };
}

describe('KnowledgeBanner scope', () => {
  it('KnowledgeBanner names the watched scope', () => {
    const { target, done } = render(['payments', 'ledger']);
    expect(text(target)).toBe('Watching 2 namespaces (ledger and payments). 1 data source is partial (k8s), so this overview may be incomplete.');
    expect(Array.from(target.querySelectorAll('bdi')).map((b) => b.textContent)).toEqual(['ledger', 'payments']);
    done();
  });

  it('KnowledgeBanner uses the singular for one namespace', () => {
    const { target, done } = render(['payments']);
    expect(text(target)).toMatch(/^Watching 1 namespace \(payments\)\. /);
    done();
  });

  it('KnowledgeBanner truncates a long scope with a title', () => {
    const { target, done } = render(['a', 'b', 'c', 'd', 'e', 'f']);
    expect(text(target)).toMatch(/^Watching 6 namespaces \(a, b, c and 3 more\)\. /);
    expect(target.querySelector('[data-testid="watched-scope"]')?.getAttribute('title')).toBe('a, b, c, d, e and f');
    done();
  });

  it('KnowledgeBanner says every watched namespace is hidden from the reader', () => {
    const { target, done } = render([], { namespacesTotal: 3 });
    expect(text(target)).toMatch(/^Watching 3 namespaces \(none you may read\)\. /);
    expect(target.querySelector('[data-testid="watched-scope"]')?.getAttribute('title')).toBe('3 namespaces you may not read');
    expect(target.querySelectorAll('bdi')).toHaveLength(0);
    done();
  });

  it('KnowledgeBanner counts names it may not show', () => {
    const { target, done } = render(['payments'], { namespacesTotal: 3 });
    expect(text(target)).toMatch(/^Watching 3 namespaces \(payments and 2 more\)\. /);
    expect(target.querySelector('[data-testid="watched-scope"]')?.getAttribute('title')).toBe('payments');
    done();
  });

  it('keeps the banner as before without a scoped source', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const sources = [{ id: 'k8s', kind: 'kubernetes', status: 'partial' }];
    const c = mount(KnowledgeBanner, { target, props: { knowledge: snapshotKnowledge({ completeness: 'partial', sources }), noun: 'overview', sources } });
    flushSync();
    expect(text(target)).toBe('The fleet snapshot is missing data. 1 data source is partial (k8s), so this overview may be incomplete.');
    unmount(c); target.remove();
  });
});
