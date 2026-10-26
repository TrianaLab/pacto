<script>
  import SearchPick from './SearchPick.svelte';

  let { label = undefined, value, options = [], onchange, ariaLabel = undefined } = $props();

  // Past a short list the facet is searchable (SearchPick) instead of a select with
  // hundreds of look-alike options. onchange keeps its event shape either way.
  const LONG = 25;
  const norm = $derived(options.map((o) => (typeof o === 'string' ? { value: o, label: o } : o)));
  const long = $derived(norm.length > LONG);
  const uid = `sel-${Math.random().toString(36).slice(2, 9)}`;
  const pick = (v) => onchange({ target: { value: v }, currentTarget: { value: v } });
</script>

{#snippet control()}
  {#if long}
    <SearchPick id={uid} {value} options={norm.filter((o) => o.value !== '')} max={LONG} placeholder={norm.find((o) => o.value === '')?.label ?? 'Any'}
      label={ariaLabel || label} onpick={pick} testid={uid} />
  {:else}
    <select {value} {onchange} aria-label={ariaLabel || label}>
      {#each norm as opt, i (i)}<option value={opt.value}>{opt.label}</option>{/each}
    </select>
  {/if}
{/snippet}

{#if label}
  <label class="filter-select">
    <span>{label}</span>
    {@render control()}
  </label>
{:else}
  {@render control()}
{/if}

<style>
  .filter-select {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: var(--text-xs);
    color: var(--c-text-3);
    min-width: 0; max-width: 100%;
  }
  .filter-select select { min-width: 0; max-width: 100%; }
</style>
