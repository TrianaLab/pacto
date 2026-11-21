<script>
  import { formatDateTime } from '../../lib/dateFormat.ts';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import Fold from '../../components/Fold.svelte';
  import LimitationsSection from '../../components/LimitationsSection.svelte';
  import WorkloadFacts from '../../components/WorkloadFacts.svelte';

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
  const hiddenCalls = $derived(d.hiddenDependencies || 0);
  const callsEmpty = $derived(hiddenCalls ? `${hiddenCalls} ${hiddenCalls === 1 ? 'call' : 'calls'} to services you cannot see`
    : d.callsObserved ? `No calls to declared services seen as of ${formatDateTime(detail.meta?.asOf)}` : 'Calls unknown: Pacto reads no traffic source here');
  // The group and repositories (509-S3) only feed the paths fold here; WorkloadFacts
  // draws them.
  const group = $derived(w.group);
  const repos = $derived(w.repositories ?? []);
  // Every value Pacto read, with its path, for the fold.
  const paths = $derived([
    ...(group && group.rule !== 'workload' ? [{ what: 'Group', from: group.from }] : []),
    ...repos.map((r) => ({ what: 'Repository', from: r.from })),
    ...(w.image ? [{ what: 'Image', from: w.image.from }] : []),
    ...ports.map((p) => ({ what: `Port ${p.port}`, from: p.from })),
    ...probes.map((p) => ({ what: `${p.kind} probe`, from: p.from })),
  ]);
</script>

<div class="inferred-entity" data-testid="inferred-workload">
  <p class="ie-ident t-body-2" data-testid="inferred-identity">
    {#if w.cluster}Cluster <bdi>{w.cluster}</bdi>, {/if}namespace <bdi>{w.namespace}</bdi>, {w.kind}. Inferred from <bdi>{src}</bdi>{#if asOf}, as of {asOf}{/if}. {#if w.declarationUnverified}Pacto could not read the Pacto targets in <bdi>{w.namespace}</bdi>, so a contract may describe this workload.{:else}No contract describes this workload.{/if}
  </p>

  <section class="ie-section" id="sec-workload" data-toc="Workload" aria-labelledby="ie-workload-h">
    <h2 id="ie-workload-h" class="t-section-title">Workload</h2>
    <WorkloadFacts workload={w} />
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
  /* Digest references and object paths carry no break point. */
  .ie-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-width: 0; }
  .ie-paths { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ie-what { color: var(--c-text-3); }
</style>
