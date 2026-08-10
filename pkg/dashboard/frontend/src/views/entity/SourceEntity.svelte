<script>
  import { formatDate } from '../../lib/dateFormat.ts';
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
    <!-- No Health row: the page header already carries this exact badge, a hundred pixels
         higher, in the same words and the same tone. Repeating it made the first fact on
         the page a restatement rather than something new, and a reader who spots the
         difference looks for one that isn't there. -->
    {#if d.kind}<div class="src-fact"><span class="src-k">Kind</span><span>{d.kind}</span></div>{/if}
    {#if d.lastSuccessfulSync}<div class="src-fact"><span class="src-k">Last successful sync</span><span>{formatDate(d.lastSuccessfulSync)}</span></div>{/if}
    {#if d.observedAt}<div class="src-fact"><span class="src-k">Observed at</span><span>{formatDate(d.observedAt)}</span></div>{/if}
    <div class="src-fact"><span class="src-k">Records</span><span>{d.revisionCount ?? 0} revisions · {d.targetCount ?? 0} operational targets</span></div>
  </section>

  {#if d.error}
    <!-- The Health badge above already says THAT the source is degraded; this row's job
         is WHY. So the human message leads and the machine code follows as a small chip
         (the same shape LimitationsList uses) instead of a bold raw enum being the first
         thing read on the page. Nothing is dropped: the exact code is still on screen and
         still selectable. -->
    <div class="src-error" role="status">
      <span class="src-error-msg">{d.error.message || 'This data source reported an error.'}</span>
      {#if d.error.code}<code class="src-error-code">{d.error.code}</code>{/if}
    </div>
  {/if}

  <PreviewSection title="Contributed entities" total={d.entities?.total ?? 0} count={d.entities?.count ?? 0} truncated={d.entities?.truncated} empty="No contributed entities.">
    <EntityRefList items={d.entities?.items ?? []} />
  </PreviewSection>

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
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
  .src-error-msg { color: var(--c-text); }
  .src-error-code {
    font-family: var(--font-mono, monospace); font-size: var(--text-xs); color: var(--c-text-3);
    background: var(--c-surface-inset); border: 1px solid var(--c-border);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
</style>
