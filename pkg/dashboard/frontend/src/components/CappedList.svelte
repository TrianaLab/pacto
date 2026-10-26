<script>
  import { formatCount } from '../lib/format.ts';

  // A list inside a page that shows its first few and a "Show all N" button,
  // so one long block never pushes the rest of the page screens down. inline
  // runs the items as a sentence, separated by CSS, never by markup spaces.
  let { items = [], max = 5, item, inline = false, label = '', testid = '' } = $props();

  let all = $state(false);
  const shown = $derived(all ? items : items.slice(0, max));
</script>

{#if items.length}
  <ul class="cl" class:cl-inline={inline} aria-label={label || undefined} data-testid={testid || undefined}>
    {#each shown as x, i (i)}<li>{@render item(x, i)}</li>{/each}
  </ul>
  {#if items.length > max}
    <button type="button" class="btn btn-sm cl-more" aria-expanded={all} onclick={() => { all = !all; }}>
      {all ? 'Show fewer' : `Show all ${formatCount(items.length)}`}
    </button>
  {/if}
{/if}

<style>
  .cl { margin: 0; padding-left: var(--sp-5); }
  .cl-inline { display: inline; padding: 0; list-style: none; }
  .cl-inline li { display: inline; }
  .cl-inline li + li::before { content: ', '; }
  .cl-more { margin-top: var(--sp-1); align-self: flex-start; }
</style>
