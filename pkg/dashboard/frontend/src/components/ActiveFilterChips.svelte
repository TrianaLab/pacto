<script>
  // Renders the active filters/focus as removable chips so filter state is visible
  // and reversible rather than hidden in component state. `chips` is
  // [{ key, label, value }]; onRemove(key) clears one, onClear() clears all.
  let { chips = [], onRemove = null, onClear = null } = $props();
</script>

{#if chips.length}
  <div class="filter-chips" aria-label="Active filters">
    {#each chips as c (c.key)}
      <span class="chip">
        <span class="chip-label">{c.label}:</span>
        <span class="chip-value">{c.value}</span>
        {#if onRemove}
          <button type="button" class="chip-x" aria-label={`Remove ${c.label} filter`} onclick={() => onRemove(c.key)}>×</button>
        {/if}
      </span>
    {/each}
    {#if onClear && chips.length > 1}
      <button type="button" class="chip-clear" onclick={onClear}>Clear all ({chips.length})</button>
    {/if}
  </div>
{/if}

<style>
  .filter-chips { display: flex; flex-wrap: wrap; gap: var(--sp-2); align-items: center; }
  .chip {
    display: inline-flex; align-items: center; gap: var(--sp-1);
    font-size: var(--text-xs); padding: 2px 4px 2px 8px; border-radius: var(--radius-sm);
    background: var(--c-accent-bg); border: 1px solid var(--c-accent-border); color: var(--c-text);
  }
  .chip-label { color: var(--c-text-3); }
  .chip-value { font-weight: 600; }
  .chip-x {
    background: none; border: none; cursor: pointer; color: var(--c-text-3);
    font-size: var(--text-md); line-height: 1; padding: 0 4px; border-radius: var(--radius-xs);
  }
  .chip-x:hover { color: var(--c-err); }
  .chip-clear {
    background: none; border: none; cursor: pointer; color: var(--c-accent);
    font-size: var(--text-xs); text-decoration: underline; padding: 2px 4px;
  }
</style>
