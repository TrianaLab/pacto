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
});
