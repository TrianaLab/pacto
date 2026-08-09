<script>
  import { fleetGraphFocusUrl } from '../../lib/router.ts';
  import { abbreviateDigests } from '../../lib/format.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import EvidenceList from '../../components/EvidenceList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';

  // The principal operational service page (requirement D). It renders ONLY the
  // product entity-detail payload (never the snapshot): owner + ownership conflicts,
  // bounded revisions / deployments / expected-dependencies / dependents previews, the
  // observed/differences relationship summary, findings attributed to exact entities,
  // recent evidence, and limitations. Every related entity uses EntityLink; every
  // preview is truncation-honest via PreviewSection.
  let { detail } = $props();
  const d = $derived(detail.service ?? {});
  const key = $derived(detail.entity?.key ?? '');
  // The graph focus is a meaningful continuation for the neighborhood previews
  // (dependencies/dependents/differences), whose exact contents live in the graph.
  const graphHref = $derived(fleetGraphFocusUrl('service', key));
</script>

<div class="svc-entity">
  <section class="se-facts">
    {#if d.domain}<div class="se-fact"><span class="se-k">Domain</span><span>{d.domain}</span></div>{/if}
    {#if d.ownership}
      <div class="se-fact">
        <span class="se-k">Owner</span>
        {#if d.ownership.ref}<EntityLink ref={d.ownership.ref} showStatus={false} showKind={false} />{:else}<span>{d.ownership.owner || 'Unowned'}</span>{/if}
      </div>
    {/if}
  </section>

  {#if d.ownership?.conflicts?.count}
    <!-- Each conflict carries a canonical revision key, so joining them into one prose
         sentence buried the owner names -- the actual point -- inside 71 characters of
         hex apiece. One conflict per line, digests abbreviated, exact key in the
         tooltip: the same facts, read as a list of revisions instead of a paragraph. -->
    <div class="se-conflicts" role="status">
      <div class="se-conflicts-head">
        <strong>Ownership conflict.</strong>
        <span>These revisions declare an owner other than {d.ownership.owner || 'the service owner'}:</span>
      </div>
      <ul class="se-conflict-list">
        {#each d.ownership.conflicts.items as c}<li title={c}>{abbreviateDigests(c)}</li>{/each}
        {#if d.ownership.conflicts.truncated}<li class="se-conflict-more">+{d.ownership.conflicts.total - d.ownership.conflicts.count} more</li>{/if}
      </ul>
    </div>
  {/if}

  <div class="se-grid">
    <PreviewSection title="Revisions" total={d.revisions?.total ?? 0} count={d.revisions?.count ?? 0} truncated={d.revisions?.truncated} empty="No known revisions.">
      <EntityRefList items={d.revisions?.items ?? []} showStatus={false} />
      <!-- Readiness is a DIMENSION of a revision, not a service-level score: it is
           declared per revision and gated per revision. Rolling it up here would invent
           a third definition of readiness, so the service page points at the one that
           already exists instead. -->
      <p class="se-hint">Readiness is declared per revision — open one to see its gate.</p>
    </PreviewSection>

    <PreviewSection title="Operational targets" total={d.deployments?.total ?? 0} count={d.deployments?.count ?? 0} truncated={d.deployments?.truncated} empty="No running target observed.">
      <EntityRefList items={d.deployments?.items ?? []} />
    </PreviewSection>

    <PreviewSection title="Expected dependencies" total={d.dependencies?.total ?? 0} count={d.dependencies?.count ?? 0} truncated={d.dependencies?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty="No declared dependencies.">
      <EntityRefList items={d.dependencies?.items ?? []} showStatus={false} />
    </PreviewSection>

    <PreviewSection title="Dependents" total={d.dependents?.total ?? 0} count={d.dependents?.count ?? 0} truncated={d.dependents?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty="Nothing depends on this service.">
      <EntityRefList items={d.dependents?.items ?? []} showStatus={false} />
    </PreviewSection>
  </div>

  {#if (d.relationships?.count ?? 0) > 0}
    <PreviewSection title="Observed traffic and differences" total={d.relationships?.total ?? null} count={d.relationships?.count ?? 0} truncated={d.relationships?.truncated} viewAllHref={graphHref} viewAllLabel="Open differences view">
      <RelationshipList items={d.relationships?.items ?? []} selfKey={key} />
    </PreviewSection>
  {/if}

  {#if (d.findings?.count ?? 0) > 0}
    <PreviewSection title="Findings" total={d.findings?.total ?? 0} count={d.findings?.count ?? 0} truncated={d.findings?.truncated}>
      <FindingList items={d.findings?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.evidence?.count ?? 0) > 0}
    <PreviewSection title="Recent evidence" total={d.evidence?.total ?? 0} count={d.evidence?.count ?? 0} truncated={d.evidence?.truncated}>
      <EvidenceList items={d.evidence?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .svc-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .se-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .se-fact { display: flex; align-items: center; gap: var(--sp-2); }
  .se-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .se-conflicts {
    display: flex; flex-direction: column; gap: var(--sp-2);
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .se-conflicts-head { display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline; }
  .se-conflict-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-conflict-list li { overflow-wrap: anywhere; }
  .se-conflict-more { color: var(--c-text-3); }
  .se-hint { margin: var(--sp-2) 0 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .se-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); }
</style>
