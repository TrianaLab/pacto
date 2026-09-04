<script>
  // `error` (string or true) switches to an error variant with an optional Retry
  // button — so a failed fetch never masquerades as a benign empty state.
  //
  // `level` is the heading level of the title. It used to be hard-coded to 3, which
  // made an empty state sitting directly under a page's h1 a skipped level (h1 -> h3),
  // a real WCAG 1.3.1 heading-order failure on any route whose whole content was an
  // empty state. It defaults to 2 -- the common case, a page or workspace with nothing
  // to show -- and a caller nesting one INSIDE an h2 section passes 3.
  // `rows` opts the loading variant into the shape of what is arriving: a divider list of
  // that many rows, sitting exactly where the real rows will. The centred four-row table
  // is what a list-shaped page used to show, and it moved every row down by a block the
  // moment the data landed. Default 0 keeps the table for callers that are not lists.
  let { title = undefined, message = undefined, loading = false, error = null, onRetry = null, level = 2, rows = 0 } = $props();

  const errorText = $derived(message || (typeof error === 'string' ? error : ''));
  // Bounded: a page size is the natural value, but nothing renders a hundred grey bars.
  const skeletonRows = $derived(Math.min(Math.max(0, Math.trunc(rows)), 12));
</script>

<div class="state-box" class:is-error={error && !loading} class:is-loading={loading && skeletonRows > 0}>
  {#if loading}
    {#if skeletonRows > 0}
      <!-- Decorative. The live text below is what a screen reader gets; a stack of empty
           divs announced one by one is noise, not progress. -->
      <ul class="pl-list sk-list" aria-hidden="true">
        {#each Array(skeletonRows) as _, i}
          <li class="sk-row">
            <div class="skeleton skeleton-line sk-name" style={`width:${[42, 30, 52, 36][i % 4]}%`}></div>
            <div class="skeleton skeleton-line sk-meta"></div>
          </li>
        {/each}
      </ul>
      <p class="visually-hidden" role="status">{message || 'Loading'}</p>
    {:else}
      <div class="skeleton-table fade-in">
        {#each Array(4) as _}
          <div class="skeleton-row">
            <div class="skeleton skeleton-line" style="width:25%"></div>
            <div class="skeleton skeleton-line" style="width:10%"></div>
            <div class="skeleton skeleton-line" style="width:15%"></div>
          </div>
        {/each}
      </div>
      {#if message}
        <p style="margin-top:var(--sp-3); color:var(--c-text-3)">{message}</p>
      {/if}
    {/if}
  {:else if error}
    <svelte:element this={`h${level}`}>{title || 'Couldn’t load'}</svelte:element>
    {#if errorText}<p>{errorText}</p>{/if}
    {#if onRetry}
      <button type="button" class="retry-btn" onclick={onRetry}>Retry</button>
    {/if}
  {:else if title || message}
    {#if title}<svelte:element this={`h${level}`}>{title}</svelte:element>{/if}
    {#if message}<p>{message}</p>{/if}
  {/if}
</div>

<style>
  .state-box {
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: var(--sp-8) var(--sp-4);
    text-align: center;
    color: var(--c-text-3);
    gap: var(--sp-3);
    /* No opacity fade-in: an in-flight opacity animation transiently dips the muted
       title/message text below the AA contrast ratio (axe samples it mid-fade), and the
       fade added no real value on a resting empty/error state. */
  }
  .state-box :is(h2, h3) {
    color: var(--c-text-2);
  }
  .state-box.is-error :is(h2, h3) {
    color: var(--c-err);
  }
  .retry-btn {
    margin-top: var(--sp-2);
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-accent);
    font: inherit;
    padding: 6px 14px;
    min-height: var(--touch-min);
    cursor: pointer;
  }
  .retry-btn:hover { background: var(--c-surface-inset); }
  .state-box p {
    max-width: 400px;
    line-height: var(--line-height);
  }
  /* The list skeleton IS the list: same frame, same dividers, same row height, so the
     real rows land on the lines the grey bars were already drawn on. */
  .state-box.is-loading {
    padding: 0;
    align-items: stretch;
    text-align: left;
  }
  .sk-list { list-style: none; margin: 0; padding: 0; }
  .sk-row {
    display: flex; align-items: center; gap: var(--sp-3);
    min-height: var(--touch-min);
    padding: var(--sp-2) var(--sp-3);
    border-bottom: 1px solid var(--c-border-subtle);
  }
  .sk-row:last-child { border-bottom: none; }
  .sk-meta { width: 4.5rem; margin-left: auto; }
  .sk-name, .sk-meta { height: 14px; margin-bottom: 0; }
  .skeleton-table {
    width: 100%;
    max-width: 600px;
  }
  .skeleton-row {
    display: flex;
    gap: var(--sp-3);
    margin-bottom: var(--sp-3);
  }
  .skeleton-row .skeleton-line {
    height: 18px;
    border-radius: var(--radius-xs);
  }
</style>
