<script>
  import { untrack } from 'svelte';

  // The filter box of a paged list. Paging without narrowing still means
  // hundreds of clicks, so every long list has one. It commits on submit to
  // the URL through href(q), never per keystroke.
  let { value = '', href, label, placeholder = 'Filter', testid = 'list-filter' } = $props();

  let draft = $state(untrack(() => value));
  $effect(() => { draft = value; });

  function submit(e) {
    e.preventDefault();
    location.hash = href(draft.trim());
  }
</script>

<form class="lf" role="search" onsubmit={submit} data-testid={testid}>
  <input class="input" type="search" bind:value={draft} {placeholder} aria-label={label} />
  <button type="submit" class="btn">Filter</button>
  {#if value}<a class="lf-clear" href={href('')}>Clear the filter</a>{/if}
</form>

<style>
  .lf { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .lf input { flex: 1 1 16rem; min-width: 0; max-width: 28rem; }
  .lf-clear { color: var(--c-accent); font-size: var(--text-sm); }
</style>
