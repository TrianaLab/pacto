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
    color: var(--c-text-3);
    font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking);
  }
  .sort-arrow {
    font-weight: 400;
    margin-left: 1px;
  }
</style>
