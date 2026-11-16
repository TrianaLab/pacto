<script>
  import ShowAll from '../components/ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';
  import { serviceUrl } from '../lib/router.ts';
  import { formatDate } from '../lib/dateFormat.ts';

  // The deliberate breaking changes a contract announces (pactoVersion 2.5): the
  // consumers each one affects, why and the date it stops applying. A consumer the
  // fleet list holds links to its page.
  let { breaks = [], services = [], open = $bindable(false), id = '' } = $props();

  const known = $derived(new Set((services ?? []).map((s) => s.name)));
  const rows = cap(25);
</script>

{#if breaks?.length > 0}
  <CollapsibleSection title="Announced breaks" count={breaks.length} bind:open {id}>
    <ul class="breaks">
      {#each rows.slice(breaks) as b, i (i)}
        <li class="break" data-testid="break-row">
          <span class="break-consumers">
            {#each b.consumers ?? [] as c, ci (c)}{#if ci > 0}, {/if}{#if known.has(c)}<a href={serviceUrl(c)}>{c}</a>{:else}{c}{/if}{/each}
          </span>
          <span class="break-reason">{b.reason}</span>
          {#if b.expires}<span class="break-expires">until {formatDate(b.expires)}</span>{/if}
        </li>
      {/each}
    </ul>
    <ShowAll cap={rows} total={breaks.length} noun="breaks" />
  </CollapsibleSection>
{:else}
  <SectionState title="Announced breaks" bind:open {id} />
{/if}

<style>
  .breaks { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .break { display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); align-items: baseline; min-width: 0; font-size: var(--text-sm); }
  .break-consumers { font-weight: 600; overflow-wrap: anywhere; min-width: 10rem; }
  .break-reason { color: var(--c-text-2); overflow-wrap: anywhere; min-width: 0; flex: 1 1 16rem; }
  .break-expires { color: var(--c-text-3); white-space: nowrap; }
</style>
