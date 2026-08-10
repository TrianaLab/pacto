<script>
  // A ranked comparison of like-for-like magnitudes (per-owner service counts,
  // per-category attention counts, per-consumer impact counts).
  //
  // Same accessibility contract as DistributionBar: the bars are decorative, every
  // exact value is printed as text, a row with a href is a keyboard-operable link,
  // and the length scale is stated so a reader knows what "full width" means. Unlike
  // a distribution these bars do NOT sum to a whole, so no percentage is offered --
  // a percentage here would invite reading unrelated magnitudes as shares.
  //
  // `scopeNote` is not decoration. When the rows come from one page of a list rather
  // than a backend-authoritative aggregate, the caller must say so here, because a
  // ranked chart drawn from a page reads exactly like a ranking of everything.
  let {
    title = '',
    level = 3,
    description = '',
    scopeNote = '',
    items = [],
    unit = '',
    unitOne = '',
    emptyLabel = 'Nothing to show yet.',
  } = $props();

  const rows = $derived(items.filter((i) => (i.value || 0) > 0));
  const max = $derived(rows.reduce((m, i) => Math.max(m, i.value || 0), 0));
  const width = (v) => (max > 0 ? Math.max(2, Math.round((v / max) * 100)) : 0);
  // A ranked chart is mostly small numbers, so "1 items" and "1 consumers" are the
  // common case, not the edge case. Callers give the singular; irregular nouns are not
  // guessed at by stripping an "s".
  const amount = (v) => `${v}${unit ? ` ${v === 1 ? unitOne || unit : unit}` : ''}`;
</script>

<figure class="hbars">
  <figcaption class="hb-cap">
    <svelte:element this={`h${level}`} class="hb-title t-subsection-title">{title}</svelte:element>
    {#if description}<p class="hb-desc">{description}</p>{/if}
    {#if scopeNote}<p class="hb-scope">{scopeNote}</p>{/if}
  </figcaption>

  {#if rows.length === 0}
    <p class="hb-empty">{emptyLabel}</p>
  {:else}
    <ul class="hb-list">
      {#each rows as r (r.label)}
        <li class="hb-row tone-{r.tone || 'info'}">
          {#if r.href}
            <a class="hb-inner" href={r.href}>
              <span class="hb-label">{r.label}</span>
              <span class="hb-track" aria-hidden="true"><span class="hb-fill" style="width: {width(r.value)}%"></span></span>
              <span class="hb-value">{amount(r.value)}</span>
            </a>
          {:else}
            <span class="hb-inner">
              <span class="hb-label">{r.label}</span>
              <span class="hb-track" aria-hidden="true"><span class="hb-fill" style="width: {width(r.value)}%"></span></span>
              <span class="hb-value">{amount(r.value)}</span>
            </span>
          {/if}
        </li>
      {/each}
    </ul>
    <p class="hb-scale">Bar length is relative to the largest value shown ({max}).</p>
  {/if}
</figure>

<style>
  .hbars { margin: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .hb-cap { display: flex; flex-direction: column; gap: 2px; }
  /* A chart title is a SUBSECTION of the block it sits in. It used to hard-code the
     section size and weight instead, so a level-3 chart title rendered larger than the
     level-2 section heading above it. Size and weight come from the role now. */
  .hb-title { margin: 0; }
  .hb-desc, .hb-scope, .hb-scale, .hb-empty { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .hb-scope { font-style: italic; }
  .hb-scale { font-size: var(--text-xs); }
  .hb-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .hb-inner {
    display: grid; grid-template-columns: minmax(6ch, 14ch) 1fr auto; align-items: center;
    gap: var(--sp-2); text-decoration: none; color: var(--c-text-2); font-size: var(--text-sm);
    min-height: var(--touch-min);
  }
  a.hb-inner:hover .hb-label, a.hb-inner:focus-visible .hb-label { text-decoration: underline; }
  .hb-label { overflow-wrap: anywhere; }
  .hb-track { display: block; height: 10px; border-radius: var(--radius-xs); background: var(--c-surface-inset); border: 1px solid var(--c-border); }
  .hb-fill { display: block; height: 100%; background: var(--tone-c, var(--c-info)); transition: width 200ms ease; }
  .hb-value { font-weight: 700; color: var(--c-text); font-variant-numeric: tabular-nums; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
  /* 320px: the label column stops competing with the bar for room. */
  @media (max-width: 30rem) {
    .hb-inner { grid-template-columns: 1fr auto; }
    .hb-track { grid-column: 1 / -1; }
  }
  @media (prefers-reduced-motion: reduce) {
    .hb-fill { transition: none; }
  }
</style>
