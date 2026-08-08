<script>
  // A bounded-preview section: the ONE place a rich entity page renders a titled,
  // truncation-honest preview (requirement D/K/B). It distinguishes an EXACT total
  // that is KNOWN from one that is UNKNOWN: some bounded backend answers deliberately
  // omit the total because it cannot be counted without violating the work bound
  // (RelationshipsPreview / RuntimePreview from an already-truncated neighborhood or
  // an early-stopped runtime walk). When the total is known it shows count-of-total;
  // when it is unknown it NEVER synthesizes one from count, scanned, page size or any
  // other bound -- a truncated preview with an unknown total says "more exist; total
  // unknown", never "X of X".
  //
  // total: a number is the EXACT known total; null/undefined means the exact total is
  // UNKNOWN (callers must pass the raw backend value, never a `?? count` fallback).
  let {
    title = '',
    total = null,
    count = 0,
    truncated = false,
    viewAllHref = '',
    viewAllLabel = 'View all',
    empty = 'None.',
    children,
  } = $props();

  const totalKnown = $derived(typeof total === 'number' && Number.isFinite(total));
</script>

<section class="ps" data-testid="preview-section">
  <div class="ps-head">
    <h2>{title}</h2>
    {#if count > 0}
      <span class="ps-count" data-testid="preview-count">{#if totalKnown}{count} of {total}{:else}{count}{/if}</span>
    {/if}
  </div>
  {#if count === 0}
    <p class="ps-empty">{empty}</p>
  {:else}
    {@render children?.()}
    {#if truncated}
      <p class="ps-more" data-testid="preview-more">
        {#if totalKnown}Showing {count} of {total}.{:else}Showing {count}. More exist; total unknown.{/if}
        {#if viewAllHref}<a href={viewAllHref}>{viewAllLabel}</a>{/if}
      </p>
    {/if}
  {/if}
</section>

<style>
  .ps { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  .ps-head { display: flex; align-items: baseline; gap: var(--sp-3); justify-content: space-between; flex-wrap: wrap; margin-bottom: var(--sp-3); }
  .ps-head h2 { margin: 0; font-size: var(--text-md); }
  .ps-count { font-size: var(--text-xs); color: var(--c-text-3); }
  .ps-empty { color: var(--c-text-3); font-size: var(--text-sm); margin: 0; }
  .ps-more { color: var(--c-text-3); font-size: var(--text-sm); margin: var(--sp-2) 0 0; }
  .ps-more a { color: var(--c-accent); text-decoration: none; }
  .ps-more a:hover { text-decoration: underline; }
</style>
