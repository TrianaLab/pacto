<script>
  import { formatDateTime } from '../../lib/dateFormat.ts';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import Fold from '../../components/Fold.svelte';
  import LimitationsSection from '../../components/LimitationsSection.svelte';
  import ProvenanceMark from '../../components/ProvenanceMark.svelte';
  import { fleetServicesUrl } from '../../lib/router.ts';

  // An inferred service: a workload no contract describes. Pacto read its image, ports
  // and probes, so the page shows those first, each with one mark saying where it was
  // read, then the declared services it was seen calling, then (collapsed) every path.
  // It has no contract, so none of the contract sections of a service page appear.
  let { detail } = $props();
  const d = $derived(detail.service ?? {});
  const w = $derived(d.workload ?? {});
  const src = $derived(w.source || 'an unknown source');
  const asOf = $derived(formatDateTime(w.observedAt || detail.meta?.asOf));
  const calls = $derived(d.calls ?? { total: 0, count: 0, items: [] });
  const ports = $derived(w.ports ?? []);
  const probes = $derived(w.probes ?? []);
  // Fields Pacto read but left out (a control character or past the bound): never
  // "none declared".
  const dropped = $derived(w.dropped ?? []);
  const droppedAny = (field) => dropped.some((f) => f === field || f.startsWith(`${field}[`));
  const hiddenCalls = $derived(d.hiddenDependencies || 0);
  const callsEmpty = $derived(hiddenCalls ? `${hiddenCalls} ${hiddenCalls === 1 ? 'call' : 'calls'} to services you cannot see`
    : d.callsObserved ? `No calls to declared services seen as of ${formatDateTime(detail.meta?.asOf)}` : 'Calls unknown: Pacto reads no traffic source here');
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
  // Every value Pacto read, with its path, for the fold.
  const paths = $derived([
    ...(group ? [{ what: 'Group', from: group.from }] : []),
    ...repos.map((r) => ({ what: 'Repository', from: r.from })),
    ...(w.image ? [{ what: 'Image', from: w.image.from }] : []),
    ...ports.map((p) => ({ what: `Port ${p.port}`, from: p.from })),
    ...probes.map((p) => ({ what: `${p.kind} probe`, from: p.from })),
  ]);
  const mark = (from) => ({ label: `Inferred service: from ${src}`, text: `Inferred from ${src}: ${from}` });
</script>

<div class="inferred-entity" data-testid="inferred-workload">
  <p class="ie-ident t-body-2" data-testid="inferred-identity">
    {#if w.cluster}Cluster <bdi>{w.cluster}</bdi>, {/if}namespace <bdi>{w.namespace}</bdi>, {w.kind}. Inferred from <bdi>{src}</bdi>{#if asOf}, as of {asOf}{/if}. {#if w.declarationUnverified}Pacto could not read the Pacto targets in <bdi>{w.namespace}</bdi>, so a contract may describe this workload.{:else}No contract describes this workload.{/if}
  </p>

  <section class="ie-section" id="sec-workload" data-toc="Workload" aria-labelledby="ie-workload-h">
    <h2 id="ie-workload-h" class="t-section-title">Workload</h2>
    <dl class="facts ie-facts">
      {#if group}
        <dt>Group</dt>
        <dd data-testid="inferred-group"><span class="ie-list">
          <bdi class="ie-text" title={group.label}>{group.label}</bdi>
          {#if group.ambiguous}<span class="t-meta ie-text">{group.ambiguous}</span>{:else if group.rule !== 'workload'}<a class="t-meta" href={fleetServicesUrl({ group: group.key, declaration: 'inferred' })}>Services in this group</a>{/if}
        </span> <ProvenanceMark label="How the cluster groups this workload" text={`Grouped by ${RULE_WORDS[group.rule] ?? group.rule}: read from ${group.from}`} /></dd>
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
  </section>

  <div data-testid="inferred-calls">
    <PreviewSection title="Calls" total={calls.total ?? 0} count={calls.count ?? 0} truncated={calls.truncated}
      empty={callsEmpty}
      help="The declared services this workload was seen calling. It has no contract, so each call is an uncontracted caller of that service.">
      <EntityRefList items={calls.items ?? []} showStatus={false} />
      {#if hiddenCalls}<p class="t-meta ie-p">And {callsEmpty}.</p>{/if}
    </PreviewSection>
  </div>

  <Fold title="Where Pacto read each value" meta={src} testid="inferred-paths">
    <p class="t-body-2 ie-p">Source <bdi>{src}</bdi>{#if asOf}, as of {asOf}{/if}.</p>
    {#if paths.length}
      <ul class="ie-paths">
        {#each paths as p (p.what + p.from)}<li><span class="ie-what">{p.what}</span> <bdi class="ie-value ie-path">{p.from}</bdi></li>{/each}
      </ul>
    {/if}
  </Fold>

  <LimitationsSection page={d.limitations} />
</div>

<style>
  .inferred-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .ie-section { display: flex; flex-direction: column; gap: var(--sp-2); }
  .ie-section h2, .ie-ident, .ie-p { margin: 0; }
  .ie-ident { color: var(--c-text-2); overflow-wrap: anywhere; }
  .ie-facts dd { display: flex; align-items: center; gap: var(--sp-2); min-width: 0; }
  .ie-list { display: flex; flex-direction: column; min-width: 0; }
  .ie-text { overflow-wrap: anywhere; min-width: 0; }
  /* Digest references and object paths carry no break point. */
  .ie-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-width: 0; }
  .ie-paths { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ie-what { color: var(--c-text-3); }
</style>
