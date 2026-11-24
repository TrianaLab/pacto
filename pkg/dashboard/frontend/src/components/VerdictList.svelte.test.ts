import { describe, it, expect } from 'vitest';
import { mount, unmount } from 'svelte';
// @ts-expect-error — Svelte component has no declaration file
import VerdictList from './VerdictList.svelte';

describe('VerdictList', () => {
  it('shows the objects and refusals of a pacto.manifests verdict', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const c = mount(VerdictList, { target, props: { verdicts: [{ gate: 'pacto.manifests', phase: 'pre', result: 'Fail',
      message: 'the version reaches outside payments: ClusterRoleBinding orders-admin is cluster-scoped',
      citation: { artifact: 'manifests', objects: 'Deployment orders, ClusterRoleBinding orders-admin', refused: 'ClusterRoleBinding orders-admin is cluster-scoped' } }] } });
    expect(target.querySelector('strong')?.textContent).toBe('Objects stay in the Stage namespace');
    const proof = [...target.querySelectorAll('.vl-cite dt')].map((dt) => [dt.textContent, dt.nextElementSibling?.textContent]);
    expect(proof).toContainEqual(['objects', 'Deployment orders, ClusterRoleBinding orders-admin']);
    expect(proof).toContainEqual(['refused', 'ClusterRoleBinding orders-admin is cluster-scoped']);
    unmount(c);
    target.remove();
  });

  it('TestVerdictListValuesLine: says which values file of this version a pacto.manifests verdict applies', () => {
    const target = document.createElement('div');
    document.body.appendChild(target);
    const digest = 'sha256:' + '5'.repeat(64);
    const c = mount(VerdictList, { target, props: { verdicts: [{ gate: 'pacto.manifests', phase: 'pre', result: 'Pass', message: 'the objects stay in production',
      citation: { artifact: 'chart', valuesFile: 'values-production.yaml', valuesDigest: digest } }, { gate: 'pacto.diff', phase: 'pre', result: 'Pass', citation: { valuesFile: 'x.yaml' } }] } });
    const lines = target.querySelectorAll('[data-testid="verdict-values"]');
    expect(lines).toHaveLength(1);
    expect(lines[0].textContent?.replace(/\s+/g, ' ')).toContain('Applies values-production.yaml from this version, digest ');
    expect(lines[0].querySelector('bdi')?.getAttribute('title')).toBe('values-production.yaml');
    expect(lines[0].querySelector('button.dt-id')?.getAttribute('title')).toBe(`${digest} (click to copy)`);
    // The line says them, so the proof does not repeat them.
    const proof = [...target.querySelectorAll('.vl-cite dt')].map((dt) => dt.textContent);
    expect(proof).toEqual(['artifact']);
    unmount(c);
    target.remove();
  });
});
