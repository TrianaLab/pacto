<script>
  let { sortBy, sortAsc, options = [], onChange } = $props();
</script>

<div class="controls-left">
  <span class="control-label">Sort</span>
  {#each options as opt}
    <button
      type="button"
      class="chip"
      class:active={sortBy === opt.value}
      onclick={() => onChange(opt.value)}
    >
      {opt.label}{#if sortBy === opt.value}<span class="sort-arrow">{sortAsc ? '↑' : '↓'}</span>{/if}
    </button>
  {/each}
</div>

<style>
  .controls-left {
    display: flex;
    align-items: center;
    gap: var(--sp-2);
    flex-wrap: wrap;
    flex: 1;
  }
  .control-label {
    font-size: var(--text-xs);
    font-weight: 500;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--c-text-3);
  }
  .sort-arrow {
    font-weight: 400;
    margin-left: 1px;
  }
</style>
