<script>
  import { sourceHealthLabel, sourceHealthTone } from '../../lib/entityLabels.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';

  // The source page (requirement G): source kind, health, last successful sync,
  // observed-at, record counts, contributed entities (navigable) and limitations.
  let { detail } = $props();
  const d = $derived(detail.source ?? {});
</script>

<div class="source-entity">
  <section class="src-facts">
    <div class="src-fact"><span class="src-k">Health</span><IdentityBadge label={sourceHealthLabel(d.health)} tone={sourceHealthTone(d.health)} /></div>
    {#if d.kind}<div class="src-fact"><span class="src-k">Kind</span><span>{d.kind}</span></div>{/if}
    {#if d.lastSuccessfulSync}<div class="src-fact"><span class="src-k">Last successful sync</span><span>{formatDate(d.lastSuccessfulSync)}</span></div>{/if}
    {#if d.observedAt}<div class="src-fact"><span class="src-k">Observed at</span><span>{formatDate(d.observedAt)}</span></div>{/if}
    <div class="src-fact"><span class="src-k">Records</span><span>{d.revisionCount ?? 0} revisions · {d.targetCount ?? 0} deployments</span></div>
  </section>

  {#if d.error}
    <div class="src-error" role="status">
      <strong>{d.error.code || 'Source error'}.</strong>
      <span>{d.error.message}</span>
    </div>
  {/if}

  <PreviewSection title="Contributed entities" total={d.entities?.total ?? 0} count={d.entities?.count ?? 0} truncated={d.entities?.truncated} empty="No contributed entities.">
    <EntityRefList items={d.entities?.items ?? []} />
  </PreviewSection>

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .source-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .src-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .src-fact { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .src-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .src-error {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline;
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-err-bg); border: 1px solid color-mix(in srgb, var(--c-err) 30%, transparent);
  }
</style>
