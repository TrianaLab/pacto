<script>
  // A bounded-preview section: the ONE place a rich entity page renders a titled,
  // truncation-honest preview (requirement D/K). It always shows count-of-total, and
  // when the preview is truncated it says so and, when a real continuation exists,
  // offers a link -- it never silently implies a preview is complete.
  let {
    title = '',
    total = 0,
    count = 0,
    truncated = false,
    viewAllHref = '',
    viewAllLabel = 'View all',
    empty = 'None.',
    children,
  } = $props();
</script>

<section class="ps" data-testid="preview-section">
  <div class="ps-head">
    <h2>{title}</h2>
    {#if total > 0}
      <span class="ps-count" data-testid="preview-count">{count} of {total}</span>
    {/if}
  </div>
  {#if count === 0}
    <p class="ps-empty">{empty}</p>
  {:else}
    {@render children?.()}
    {#if truncated}
      <p class="ps-more">
        Showing {count} of {total}.
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
