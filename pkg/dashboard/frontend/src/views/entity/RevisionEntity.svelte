<script>
  import { retrievabilityLabel, retrievabilityTone } from '../../lib/entityLabels.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import CopyableIdentifier from '../../components/CopyableIdentifier.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';

  // The revision page (requirement E): an IMMUTABLE version of the service contract,
  // not a deployment. It renders the contract facets (interfaces/configs/policies/
  // capabilities/tools/skills/docs), declared dependencies, readiness, validation,
  // exact + inferred targets, and previous/next revisions. Content retrievability is
  // shown as its OWN dimension: the revision is immutable, but its content may not be
  // retrievable -- we never call non-retrievable content "immutable" just because the
  // revision is known.
  let { detail } = $props();
  const d = $derived(detail.revision ?? {});
  const id = $derived(d.identity ?? {});
  const facets = $derived([
    { k: 'Interfaces', v: d.interfaces ?? 0 },
    { k: 'Configurations', v: d.configurations ?? 0 },
    { k: 'Policies', v: d.policies ?? 0 },
    { k: 'Capabilities', v: d.capabilities ?? 0 },
  ]);
  const r = $derived(d.readiness ?? null);
</script>

<div class="rev-entity">
  <section class="re-facts">
    <div class="re-fact"><span class="re-k">Service</span><EntityLink ref={d.service} showStatus={false} /></div>
    {#if d.version}<div class="re-fact"><span class="re-k">Version</span><span>{d.version}</span></div>{/if}
    {#if d.pactoVersion}<div class="re-fact"><span class="re-k">pactoVersion</span><span>{d.pactoVersion}</span></div>{/if}
    <div class="re-fact"><span class="re-k">Valid</span><span>{d.valid ? 'Yes' : 'No'}</span></div>
    <div class="re-fact"><span class="re-k">Content</span><IdentityBadge label={retrievabilityLabel(id.identityClass, id.retrievable)} tone={retrievabilityTone(id.identityClass, id.retrievable)} /></div>
  </section>

  {#if id.digest || id.resolvedRef || id.requestedRef}
    <section class="re-identity">
      {#if id.digest}<div class="re-idrow"><span class="re-k">Digest</span><CopyableIdentifier value={id.digest} /></div>{/if}
      {#if id.resolvedRef}<div class="re-idrow"><span class="re-k">Resolved ref</span><CopyableIdentifier value={id.resolvedRef} /></div>{/if}
      {#if id.requestedRef && id.requestedRef !== id.resolvedRef}<div class="re-idrow"><span class="re-k">Requested ref</span><CopyableIdentifier value={id.requestedRef} /></div>{/if}
    </section>
  {/if}

  {#if r}
    <section class="re-readiness">
      <div class="rr-head">
        <h2>Readiness</h2>
        <IdentityBadge label={r.passing ? 'Passing' : 'Not passing'} tone={r.passing ? 'ok' : 'warn'} />
      </div>
      <p class="rr-line">Score {r.score} / {r.minScore} required · {r.doneCount} done · {r.partialCount} partial · {r.notDoneCount} not done{r.deferredCount ? ` · ${r.deferredCount} deferred` : ''}{r.expired ? ' · expired' : ''}</p>
    </section>
  {/if}

  <div class="re-facets">
    {#each facets as f}<span class="re-facet"><b>{f.v}</b> {f.k}</span>{/each}
    {#if (d.tools?.total ?? 0) > 0}<span class="re-facet"><b>{d.tools.total}</b> Tools</span>{/if}
    {#if (d.skills?.total ?? 0) > 0}<span class="re-facet"><b>{d.skills.total}</b> Skills</span>{/if}
    {#if (d.docs?.total ?? 0) > 0}<span class="re-facet"><b>{d.docs.total}</b> Docs</span>{/if}
  </div>

  {#if (d.validation?.count ?? 0) > 0}
    <PreviewSection title="Validation findings" total={d.validation?.total ?? 0} count={d.validation?.count ?? 0} truncated={d.validation?.truncated}>
      <FindingList items={d.validation?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.dependencies?.count ?? 0) > 0}
    <PreviewSection title="Declared dependencies" total={d.dependencies?.total ?? d.dependencies?.count ?? 0} count={d.dependencies?.count ?? 0} truncated={d.dependencies?.truncated}>
      <RelationshipList items={d.dependencies?.items ?? []} />
    </PreviewSection>
  {/if}

  <div class="re-grid">
    {#if (d.exactTargets?.count ?? 0) > 0}
      <PreviewSection title="Exact-match deployments" total={d.exactTargets?.total ?? 0} count={d.exactTargets?.count ?? 0} truncated={d.exactTargets?.truncated}>
        <EntityRefList items={d.exactTargets?.items ?? []} />
      </PreviewSection>
    {/if}
    {#if (d.inferredTargets?.count ?? 0) > 0}
      <PreviewSection title="Inferred-match deployments" total={d.inferredTargets?.total ?? 0} count={d.inferredTargets?.count ?? 0} truncated={d.inferredTargets?.truncated}>
        <EntityRefList items={d.inferredTargets?.items ?? []} />
      </PreviewSection>
    {/if}
  </div>

  {#if d.previous || d.next}
    <section class="re-adjacent">
      {#if d.previous}<div class="re-adj"><span class="re-k">Previous revision</span><EntityLink ref={d.previous} showStatus={false} /></div>{/if}
      {#if d.next}<div class="re-adj"><span class="re-k">Next revision</span><EntityLink ref={d.next} showStatus={false} /></div>{/if}
    </section>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .rev-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .re-facts, .re-identity, .re-adjacent { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .re-fact, .re-idrow, .re-adj { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .re-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .re-readiness { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); }
  .rr-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .rr-head h2 { margin: 0; font-size: var(--text-md); }
  .rr-line { color: var(--c-text-2); font-size: var(--text-sm); margin: var(--sp-2) 0 0; }
  .re-facets { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .re-facet { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); padding: 2px 10px; border-radius: var(--radius-xs); }
  .re-facet b { color: var(--c-text); }
  .re-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); }
</style>
