<script>
  import { pageView, offsetOfPage, pageSizes } from '../lib/paging.ts';
  import { formatCount } from '../lib/format.ts';

  // The one pager every long list uses. The server answers the page; every
  // move is a link built by href(offset, limit), so the page lives in the URL
  // and Back returns to it.
  let { page, href, label = 'Pages', noun = 'items', testid = 'pager' } = $props();

  const v = $derived(pageView(page));
  const sizes = $derived(pageSizes(v.limit));
  let jump = $state('');

  function go(e) {
    e.preventDefault();
    location.hash = href(offsetOfPage(Number(jump), v.pages, v.limit), v.limit);
    jump = '';
  }
</script>

{#if v.total > 0}
  <nav class="pl-pager" aria-label={label} data-testid={testid}>
    <span class="pl-range" data-testid={`${testid}-range`}>
      {#if v.pages > 1}Showing {formatCount(v.from)}–{formatCount(v.to)} of {formatCount(v.total)} {noun}{:else}{formatCount(v.total)} {noun}{/if}
    </span>
    {#if v.pages > 1}
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
