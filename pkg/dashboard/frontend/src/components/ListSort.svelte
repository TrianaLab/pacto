<script>
  // The order a paged list is in. The server sorts before it pages, so every page
  // agrees with the order, and the choice lives in the URL through href(sort).
  let { value, href, options = [['name', 'Name'], ['health', 'Least healthy first']], testid = 'list-sort' } = $props();
</script>

<label class="ls">
  <span>Order</span>
  <select {value} onchange={(e) => { location.hash = href(e.currentTarget.value); }} data-testid={testid}>
    {#each options as [v, text] (v)}<option value={v}>{text}</option>{/each}
  </select>
</label>

<style>
  .ls { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
</style>
