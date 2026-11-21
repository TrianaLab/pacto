<script>
  import ProvenanceMark from './ProvenanceMark.svelte';
  import { fleetServicesUrl } from '../lib/router.ts';

  // What Pacto read from one workload: its group, repository, image, ports and probes,
  // each with one mark saying where it was read. The inferred service page leads with
  // it, and a declared service's provenance fold shows it for the workload it replaced.
  let { workload } = $props();
  const w = $derived(workload ?? {});
  const src = $derived(w.source || 'an unknown source');
  const ports = $derived(w.ports ?? []);
  const probes = $derived(w.probes ?? []);
  // Fields Pacto read but left out (a control character or past the bound): never
  // "none declared".
  const dropped = $derived(w.dropped ?? []);
  const droppedAny = (field) => dropped.some((f) => f === field || f.startsWith(`${field}[`));
  const portWords = (p) => [p.port, p.name, p.protocol].filter(Boolean).join(' ');
  const probeWords = (p) => [p.path, p.port ? `port ${p.port}` : ''].filter(Boolean).join(' ') || 'no HTTP path';
  // How the cluster already groups the workload (509-S3), and the repository a GitOps
  // object names for that group. Only the GitOps rules name a repository.
  const group = $derived(w.group);
  const repos = $derived(w.repositories ?? []);
  const RULE_WORDS = { argocd: 'Argo CD Application', 'flux-kustomization': 'Flux Kustomization', 'flux-helmrelease': 'Flux HelmRelease', helm: 'Helm release',
    'part-of': 'the app.kubernetes.io/part-of label', instance: 'the app.kubernetes.io/instance label', workload: 'the workload itself' };
  const REPO_RULES = ['argocd', 'flux-kustomization', 'flux-helmrelease'];
  const showRepo = $derived(REPO_RULES.includes(group?.rule) || repos.length > 0 || !!w.repositoryUnknown);
  // The workload's own group was read from no path: its tip says why it is ungrouped.
  const groupTip = $derived(!group ? '' : group.ambiguous ? `Ungrouped: ${group.ambiguous}`
    : group.rule === 'workload' ? `Not grouped: ${group.from}` : `Grouped by ${RULE_WORDS[group.rule] ?? group.rule}: read from ${group.from}`);
  const mark = (from) => ({ label: `Inferred service: from ${src}`, text: `Inferred from ${src}: ${from}` });
</script>

<dl class="facts ie-facts" data-testid="workload-facts">
  {#if group}
    <dt>Group</dt>
    <dd data-testid="inferred-group"><span class="ie-list">
      <bdi class="ie-text" title={group.label}>{group.label}</bdi>
      {#if group.ambiguous}<span class="t-meta ie-text">{group.ambiguous}</span>{:else if droppedAny('group')}<span class="t-meta ie-text">The group was read but not shown (invalid value)</span>{:else if group.rule !== 'workload'}<a class="t-meta" href={fleetServicesUrl({ group: group.key, declaration: 'inferred' })}>Services in this group</a>{/if}
    </span> <ProvenanceMark label="How the cluster groups this workload" text={groupTip} /></dd>
  {/if}
  {#if showRepo}
    <dt>Repository</dt>
    <dd data-testid="inferred-repository"><span class="ie-list">
      {#each repos as r (r.from)}
        <span>{#if r.url.startsWith('https://')}<a href={r.url} rel="noopener noreferrer" target="_blank"><bdi class="ie-value" title={r.url}>{r.url}</bdi></a>{:else}<bdi class="ie-value" title={r.url}>{r.url}</bdi>{/if}{#if r.path} <span class="t-meta">path</span> <bdi class="ie-value" title={r.path}>{r.path}</bdi>{/if}</span>
      {/each}
      {#if w.repositoryUnknown || !repos.length}<span class="t-meta ie-text">Repository unknown{w.repositoryUnknown ? `: ${w.repositoryUnknown}` : ''}</span>{/if}
    </span>{#if repos.length} <ProvenanceMark label="Where Pacto read the repository" text={`Read from ${repos.map((r) => r.from).join(', ')}`} />{/if}</dd>
  {/if}
  <dt>Image</dt>
  <dd>{#if w.image}<bdi class="ie-value" title={w.image.value} data-testid="inferred-image">{w.image.value}</bdi> <ProvenanceMark {...mark(w.image.from)} />{:else}<span class="t-meta">{droppedAny('image') ? 'The image was read but not shown (invalid value)' : 'No image was read'}</span>{/if}</dd>
  <dt>Ports</dt>
  <dd>{#if ports.length}<span class="ie-list">{#each ports as p (p.from)}<span><bdi class="ie-value">{portWords(p)}</bdi></span>{/each}</span> <ProvenanceMark {...mark(ports.map((p) => p.from).join(', '))} />{:else}<span class="t-meta">{droppedAny('ports') ? 'Ports were read but not shown (invalid value)' : 'No ports read'}</span>{/if}</dd>
  <dt>Probes</dt>
  <dd>{#if probes.length}<span class="ie-list">{#each probes as p (p.from)}<span>{p.kind}: <bdi class="ie-value">{probeWords(p)}</bdi></span>{/each}</span> <ProvenanceMark {...mark(probes.map((p) => p.from).join(', '))} />{:else}<span class="t-meta">{droppedAny('probes') ? 'Probes were read but not shown (invalid value)' : 'No HTTP probe read on the main container'}</span>{/if}</dd>
</dl>

<style>
  .ie-facts dd { display: flex; align-items: center; gap: var(--sp-2); min-width: 0; }
  .ie-list { display: flex; flex-direction: column; min-width: 0; }
  .ie-text { overflow-wrap: anywhere; min-width: 0; }
  /* Digest references and object paths carry no break point. */
  .ie-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-width: 0; }
</style>
