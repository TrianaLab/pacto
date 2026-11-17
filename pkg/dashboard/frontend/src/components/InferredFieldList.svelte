<script>
  import HelpTip from './HelpTip.svelte';
  import { formatCount } from '../lib/format.ts';

  // Values Pacto inferred and keeps apart from the contract. Each row is muted and
  // carries one discreet mark; where the value came from and how sure Pacto is sit in
  // the mark's note, never in a chip on every row. The first rows show, the rest are one
  // disclosure away. `total` counts every field when the server bounded `items`.
  let { items = [], total = 0, limit = 5, unreadable = false } = $props();

  const shown = $derived(items.slice(0, limit));
  const rest = $derived(items.slice(limit));
  const all = $derived(Math.max(total, items.length));
  const note = (f) => `Inferred, not declared. Source: ${f.source}.${f.confidence ? ` Confidence: ${f.confidence}.` : ''}`;
</script>

{#snippet row(f)}
  <li class="ifl-row" data-testid="inferred-field">
    <code><bdi>{f.field}</bdi></code>
    <span class="ifl-value"><bdi>{f.value}</bdi><HelpTip label={`inferred ${f.field}`} text={note(f)} /></span>
  </li>
{/snippet}

<div class="ifl t-meta">
  {#if unreadable}
    <p class="ifl-note">Pacto could not read the inferred record</p>
  {:else}
    <ul class="ifl-list">{#each shown as f (`${f.field}:${f.source}`)}{@render row(f)}{/each}</ul>
    {#if all > shown.length}
      <details class="ifl-more disclosure">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{formatCount(all - shown.length)} more</summary>
        <ul class="ifl-list">{#each rest as f (`${f.field}:${f.source}`)}{@render row(f)}{/each}</ul>
        {#if total > items.length}<p class="ifl-note">Showing {formatCount(items.length)} of {formatCount(total)}.</p>{/if}
      </details>
    {/if}
  {/if}
</div>

<style>
  .ifl { container-type: inline-size; min-inline-size: 0; }
  .ifl-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  /* Stacked on a narrow container: the value wraps under the field. */
  .ifl-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 0 var(--sp-3); align-items: baseline; min-inline-size: 0; }
  @container (min-width: 32rem) { .ifl-row { grid-template-columns: minmax(12ch, 40%) minmax(0, 1fr); } }
  .ifl-row code, .ifl-value { overflow-wrap: anywhere; min-inline-size: 0; }
  .ifl-row code { font-size: var(--text-sm); color: inherit; }
  .ifl-value { display: inline-flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .ifl-note { margin: 0; }
  .ifl-more { margin-top: var(--sp-2); }
  .ifl-more .ifl-list, .ifl-more .ifl-note { margin-top: var(--sp-1); }
</style>
