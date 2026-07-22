<script>
  let { trail = [] } = $props();
</script>

{#if trail.length}
  <nav class="breadcrumbs" aria-label="Breadcrumb">
    {#each trail as crumb, i}
      {#if i > 0}<span class="bc-sep" aria-hidden="true">›</span>{/if}
      {#if crumb.href && i < trail.length - 1}
        <a href={crumb.href} class="bc-link">{crumb.label}</a>
      {:else}
        <span class="bc-current" aria-current="page">{crumb.label}</span>
      {/if}
    {/each}
  </nav>
{/if}

<style>
  .breadcrumbs {
    display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap;
    font-size: var(--text-sm); margin-bottom: var(--sp-4);
  }
  .bc-link { color: var(--c-text-3); text-decoration: none; }
  .bc-link:hover { color: var(--c-text); text-decoration: underline; }
  .bc-sep { color: var(--c-text-3); }
  .bc-current { color: var(--c-text); font-weight: 500; }
</style>
