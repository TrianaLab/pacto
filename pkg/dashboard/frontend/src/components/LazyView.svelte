<script>
  // A route view loaded on first use, so the libraries only it needs (cytoscape,
  // d3) stay out of the first download. load caches its import, so a re-render
  // never loads the module twice.
  let { load, ...props } = $props();
</script>

{#await load() then view}
  <view.default {...props} />
{:catch err}
  <p class="lazy-error" role="alert">This view could not be loaded ({err?.message ?? err}). Reload the page to try again.</p>
{/await}

<style>
  .lazy-error { color: var(--c-err); padding: var(--sp-4); }
</style>
