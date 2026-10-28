<script>
  import { formatCount } from '../lib/format.ts';

  // The button a capped table or block offers: Show all N, then Show fewer. Nothing
  // when everything already fits.
  let { cap, total = 0, noun = 'items', testid = undefined } = $props();
</script>

{#if total > cap.max}
  <button type="button" class="btn btn-sm show-all" aria-expanded={cap.all} onclick={() => cap.toggle()} data-testid={testid}>
    {cap.all ? 'Show fewer' : `Show all ${formatCount(total)} ${noun}`}
  </button>
{/if}

<style>
  .show-all { margin-top: var(--sp-2); }
</style>
