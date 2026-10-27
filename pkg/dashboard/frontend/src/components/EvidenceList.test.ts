import { describe, it, expect } from 'vitest';
// @ts-expect-error — Svelte components have no declaration files
import { groupEvidence } from './EvidenceList.svelte';

describe('groupEvidence', () => {
  it('prints a shared environment and time once per run', () => {
    const t = (label: string, at: string) => ({ target: { kind: 'target', key: label, label }, at });
    const g = groupEvidence([
      t('prod/Deployment/a', '1'), t('prod/Deployment/b', '1'), t('prod/Deployment/c', '2'), t('dev/Deployment/d', '2'),
    ], (x: string) => `t${x}`);
    expect(g.map((x: { prefix: string; at: string; items: unknown[] }) => [x.prefix, x.at, x.items.length])).toEqual([
      ['prod/Deployment', 't1', 2], ['prod/Deployment', 't2', 1], ['dev/Deployment', 't2', 1],
    ]);
    expect(g[0].items.map((i: { name: string }) => i.name)).toEqual(['a', 'b']);
  });
});
