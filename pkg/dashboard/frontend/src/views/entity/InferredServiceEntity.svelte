<script>
  import { formatDateTime } from '../../lib/dateFormat.ts';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import Fold from '../../components/Fold.svelte';
  import LimitationsSection from '../../components/LimitationsSection.svelte';
  import WorkloadFacts from '../../components/WorkloadFacts.svelte';
  import DeclarePanel from '../../components/DeclarePanel.svelte';
  import InferredFact from '../../components/InferredFact.svelte';
  import { nextActionWords, businessDomainLabel, UNKNOWN_DOMAIN } from '../../lib/entityLabels.ts';

  // An inferred service: a workload no contract describes. Pacto read its image, ports
  // and probes, so the page shows those first, each with one mark saying where it was
  // read, then the declared services it was seen calling, then (collapsed) every path.
  // It has no contract, so none of the contract sections of a service page appear.
  // canDeclare: the host plans declarations for this caller (capabilities.declare);
  // declareQuery: declare=1 opens the Declare panel, signed is the intent a signing
  // login came back with.
  let { detail, canDeclare = false, declareQuery = {} } = $props();
  // The panel asks for its plan only once the fold is open.
  let toggled = $state(null);
  const declareOpen = $derived(toggled ?? !!declareQuery.open);
  const d = $derived(detail.service ?? {});
  const w = $derived(d.workload ?? {});
  const src = $derived(w.source || 'an unknown source');
  const asOf = $derived(formatDateTime(w.observedAt || detail.meta?.asOf));
  const calls = $derived(d.calls ?? { total: 0, count: 0, items: [] });
  const ports = $derived(w.ports ?? []);
  const probes = $derived(w.probes ?? []);
  // The owner and business domain a source outside every contract reports (a catalog).
  const inferredOwner = $derived(d.ownership?.inferred);
  const inferredDomain = $derived(d.inferredBusinessDomain);
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

  {#if inferredOwner || inferredDomain}
    <p class="ie-p ie-facts t-body-2" data-testid="inferred-catalog">
      {#if inferredOwner}<span>Owner unknown<InferredFact fact={inferredOwner} what="owner" /></span>{/if}
      {#if inferredDomain}<span>{businessDomainLabel(UNKNOWN_DOMAIN)}<InferredFact fact={inferredDomain} what="domain" /></span>{/if}
    </p>
  {/if}

  {#if canDeclare && d.nextAction?.verb === 'declare'}
    <Fold title="Declare this service" testid="declare-panel" open={!!declareQuery.open} ontoggle={(e) => (toggled = e.currentTarget.open)}>
      <DeclarePanel serviceKey={detail.entity?.key ?? ''} open={declareOpen} signed={declareQuery.signed ?? ''} />
    </Fold>
  {:else if d.nextAction?.verb === 'declare'}
    <!-- This host plans no declaration for this caller: the next step is the command a person runs. -->
    <p class="ie-p ie-next t-body-2" data-testid="inferred-next-action">{nextActionWords(d.nextAction)}</p>
  {/if}

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
  .ie-next { overflow-wrap: anywhere; }
  .ie-facts { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-4); color: var(--c-text-2); }
  .ie-facts > span { display: inline-flex; align-items: center; min-inline-size: 0; max-inline-size: 100%; }
  /* Digest references and object paths carry no break point. */
  .ie-value { font-family: var(--font-mono); font-size: 0.95em; overflow-wrap: anywhere; min-width: 0; }
  .ie-paths { margin: 0; padding: 0; list-style: none; display: flex; flex-direction: column; gap: var(--sp-1); }
  .ie-what { color: var(--c-text-3); }
</style>
