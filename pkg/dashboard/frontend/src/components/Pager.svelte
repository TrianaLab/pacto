<script>
  import { pageView, offsetOfPage, pageSizes } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';

  // The one pager every long list uses. The server answers the page; every
  // move is a link built by href(offset, limit), so the page lives in the URL
  // and Back returns to it.
  // count: how many rows this page holds, when known. None on a page the server
  // matched rows for means the link went past the end, and the pager says so with
  // a way back instead of an empty list beside a count.
  let { page, href, label = 'Pages', noun = 'items', testid = 'pager', count = undefined } = $props();

  const v = $derived(pageView(page));
  // "1 service", not "1 services": every noun a list passes is a plain plural.
  const word = $derived(v.total === 1 ? noun.replace(/s$/, '') : noun);
  // An offset off the page grid (a link into the middle) is paged even when the
  // whole list fits on one page, so its way back to the start shows.
  const paged = $derived(v.pages > 1 || v.from > 1);
  const sizes = $derived(pageSizes(v.limit));
  let jump = $state('');

  function go(e) {
    e.preventDefault();
    location.hash = href(offsetOfPage(Number(jump), v.pages, v.limit), v.limit);
    jump = '';
  }
</script>

{#if count === 0 && v.total > 0}
  <p class="pl-past-end" role="status" data-testid={`${testid}-past-end`}>
    This page is past the end of the {formatCount(v.total)} {word}. <a href={href(v.last ?? 0, v.limit)}>Go to the last page</a>
  </p>
{:else if v.total > 0}
  <nav class="pl-pager" aria-label={label} data-testid={testid}>
    <span class="pl-range" data-testid={`${testid}-range`}>
      {#if paged}Showing {formatCount(v.from)}–{formatCount(v.to)} of {formatCount(v.total)} {word}{:else}{formatCount(v.total)} {word}{/if}
    </span>
    {#if paged}
      <div class="pl-pager-btns">
        {#each [['First', v.first, 'first'], ['Previous', v.prev, 'prev'], ['Next', v.next, 'next'], ['Last', v.last, 'last']] as [text, to, rel] (rel)}
          {#if to === null}
            <span class="pl-page disabled" aria-disabled="true">{text}</span>
          {:else}
            <a class="pl-page" href={href(to, v.limit)} data-testid={`${testid}-${rel}`} rel={rel === 'prev' || rel === 'next' ? rel : undefined}>{text}</a>
          {/if}
        {/each}
      </div>
      <form class="pl-jump" onsubmit={go}>
        <label>Page <input class="input" type="number" min="1" max={v.pages} placeholder={String(v.page)} bind:value={jump}
          aria-label={`Page number, 1 to ${v.pages}`} data-testid={`${testid}-jump`} /> of {formatCount(v.pages)}</label>
      </form>
    {/if}
    {#if v.total > sizes[0]}
      <label class="pl-size">Per page
        <select value={v.limit} onchange={(e) => { location.hash = href(0, Number(e.currentTarget.value)); }} data-testid={`${testid}-size`}>
          {#each sizes as n (n)}<option value={n}>{n}</option>{/each}
        </select>
      </label>
    {/if}
  </nav>
{/if}

<style>
  .pl-jump label, .pl-size { display: inline-flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-3); }
  .pl-jump input { width: 5.5rem; }
</style>
