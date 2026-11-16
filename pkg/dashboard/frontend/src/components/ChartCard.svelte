<script>
  import ShowAll from './ShowAll.svelte';
  import { TABLE_PAGE } from '../lib/metrics.ts';
  import { formatCount } from '../lib/format.ts';

  // One Metrics chart (#597): a titled figure, the question it answers and a "Show as
  // table" view of the same numbers (lib/metrics.ts tableRows), so nothing depends on
  // hovering. The toggle is local to the card. Tables page locally (no URL pager, no
  // data-view-total: figureLinkTotals reads that first) and stack into cards in a
  // narrow frame through the shared data-table container query.
  let { title = '', question = '', tables = [], chart = undefined, foot = undefined, testid = undefined } = $props();

  const id = $props.id();
  let asTable = $state(false);
  let pages = $state({});
  let all = $state({});

  const shown = (t, i) => (all[i] ? t.rows : t.rows.slice((pages[i] ?? 0) * TABLE_PAGE, ((pages[i] ?? 0) + 1) * TABLE_PAGE));
  const capOf = (i) => ({ all: !!all[i], max: TABLE_PAGE, toggle: () => { all[i] = !all[i]; pages[i] = 0; } });
</script>

<section class="card cc" data-fig aria-labelledby={id} data-testid={testid}>
  <div class="cc-head">
    <div class="cc-titles">
      <h2 {id} class="cc-title">{title}</h2>
      {#if question}<p class="cc-q">{question}</p>{/if}
    </div>
    {#if tables.length}
      <button type="button" class="btn btn-sm cc-toggle" aria-pressed={asTable} onclick={() => { asTable = !asTable; }}>
        {asTable ? 'Show as chart' : 'Show as table'}
      </button>
    {/if}
  </div>

  {#if asTable}
    {#each tables as t, i (i)}
      {@const page = pages[i] ?? 0}
      <div class="data-scroll">
        <table class="data-table cc-table">
          <caption class="visually-hidden">{t.caption}</caption>
          <thead><tr>{#each t.head as h (h)}<th scope="col">{h}</th>{/each}</tr></thead>
          <tbody>
            {#each shown(t, i) as row, r (r)}
              <tr>
                <th scope="row" title={row[0].title ?? row[0].text} class:cc-unk={row[0].unknown}><bdi class="cell-clip">{row[0].text}</bdi></th>
                {#each row.slice(1) as c, j (j)}
                  <td data-label={t.head[j + 1]} class:cc-unk={c.unknown}>{#if c.href}<a href={c.href} data-lists={c.lists}>{c.text}</a>{:else}{c.text}{/if}</td>
                {/each}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if t.rows.length > TABLE_PAGE}
        <div class="cc-pager">
          {#if !all[i]}
            <button type="button" class="btn btn-sm" disabled={page === 0} onclick={() => { pages[i] = page - 1; }}>Previous</button>
            <span class="t-meta">{formatCount(page * TABLE_PAGE + 1)} to {formatCount(Math.min((page + 1) * TABLE_PAGE, t.rows.length))} of {formatCount(t.rows.length)}</span>
            <button type="button" class="btn btn-sm" disabled={(page + 1) * TABLE_PAGE >= t.rows.length} onclick={() => { pages[i] = page + 1; }}>Next</button>
          {/if}
          <ShowAll cap={capOf(i)} total={t.rows.length} noun="rows" />
        </div>
      {/if}
    {/each}
  {:else}
    {@render chart?.()}
  {/if}
  {@render foot?.()}
</section>

<style>
  .cc { container-type: inline-size; display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; padding: var(--sp-4); }
  .cc-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--sp-1) var(--sp-3); }
  .cc-titles { min-width: 0; }
  .cc-title { margin: 0; font: italic 600 var(--text-md)/1.35 var(--font-display); overflow-wrap: anywhere; }
  .cc-q { margin: 2px 0 0; font-size: var(--text-xs); color: var(--c-text-3); }
  .cc-toggle { white-space: nowrap; }
  .cc-toggle[aria-pressed='true'] { color: var(--c-accent); border-color: var(--c-accent-border); background: var(--c-accent-bg); }
  @media (pointer: coarse) { .cc-toggle { min-height: 44px; } }
  .cc-table { font-variant-numeric: tabular-nums; }
  .cc-unk { font-style: italic; color: var(--c-text-2); }
  .cc-pager { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); }
  .cc-pager :global(.show-all) { margin-top: 0; }
</style>
