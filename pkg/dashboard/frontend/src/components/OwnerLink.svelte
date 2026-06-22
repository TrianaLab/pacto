<script>
  import { ownerDisplay, ownerKey } from '../lib/format.ts';
  import { ownerUrl } from '../lib/router.ts';
  import { setFilter } from '../lib/filters.svelte.ts';

  let { owner } = $props();

  let displayName = $derived(ownerDisplay(owner));
</script>

{#if displayName}
  <a
    href={ownerUrl(ownerKey(owner))}
    onclick={(e) => {
      e.stopPropagation();
      setFilter('owner', ownerKey(owner));
    }}
  >
    {displayName}
  </a>
{:else}
  <span class="text-dim">(unowned)</span>
{/if}

<style>
  a {
    color: var(--c-text-3);
    font-size: var(--text-xs);
    text-decoration: none;
  }
  a:hover {
    color: var(--c-text-2);
    text-decoration: underline;
  }
  .text-dim {
    color: var(--c-text-3);
  }
</style>
