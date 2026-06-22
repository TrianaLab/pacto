<script>
  let { label = undefined, value, options = [], onchange, ariaLabel = undefined } = $props();
</script>

{#if label}
  <label class="filter-select">
    <span>{label}</span>
    <select {value} {onchange} aria-label={ariaLabel || label}>
      {#each options as opt}
        {#if typeof opt === 'string'}
          <option value={opt}>{opt}</option>
        {:else}
          <option value={opt.value}>{opt.label}</option>
        {/if}
      {/each}
    </select>
  </label>
{:else}
  <select {value} {onchange} aria-label={ariaLabel}>
    {#each options as opt}
      {#if typeof opt === 'string'}
        <option value={opt}>{opt}</option>
      {:else}
        <option value={opt.value}>{opt.label}</option>
      {/if}
    {/each}
  </select>
{/if}

<style>
  .filter-select {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--text-xs);
    color: var(--c-text-3);
  }
</style>
