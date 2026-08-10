<script>
  // A proportional distribution across a FINITE, exhaustive set of buckets.
  //
  // The accessibility contract this component exists to enforce, once, for every
  // product surface that draws a proportion:
  //   * the bar itself is decorative (aria-hidden) -- every number it encodes is
  //     printed as text in the legend beside it, so nothing is conveyed by colour or
  //     length alone and no screen reader has to interpret a shape;
  //   * the exact value is always shown, never only a percentage or a tooltip;
  //   * the figure has a real accessible name from its own caption heading;
  //   * a bucket with a href becomes a link, so every drill-down is keyboard-operable;
  //   * width transitions are dropped under prefers-reduced-motion.
  //
  // `total` MUST be the backend-authoritative population, not the sum of whatever
  // buckets happened to be passed: a distribution whose denominator is a truncated
  // preview would silently state a fleet-wide proportion it cannot know. When total
  // exceeds the buckets given, the remainder is shown as an explicit unclassified
  // slice rather than being absorbed into the last bucket.
  let {
    title = '',
    level = 3,
    description = '',
    scopeNote = '',
    segments = [],
    total = null,
    emptyLabel = 'Nothing to show yet.',
  } = $props();

  const sum = $derived(segments.reduce((n, s) => n + (s.value || 0), 0));
  const denom = $derived(typeof total === 'number' && total > sum ? total : sum);
  const rest = $derived(Math.max(0, denom - sum));
  const rows = $derived([
    ...segments.map((s) => ({ ...s, value: s.value || 0 })),
    ...(rest > 0 ? [{ label: 'Unclassified', value: rest, tone: 'neutral' }] : []),
  ].filter((s) => s.value > 0));
  // One decimal, and only as a companion to the exact count -- never as a replacement.
  const pct = (v) => (denom > 0 ? Math.round((v / denom) * 1000) / 10 : 0);
</script>

<figure class="dist">
  <figcaption class="dist-cap">
    <svelte:element this={`h${level}`} class="dist-title t-subsection-title">{title}</svelte:element>
    {#if description}<p class="dist-desc">{description}</p>{/if}
    {#if scopeNote}<p class="dist-scope">{scopeNote}</p>{/if}
  </figcaption>

  {#if rows.length === 0}
    <p class="dist-empty">{emptyLabel}</p>
  {:else}
    <div class="dist-bar" aria-hidden="true">
      {#each rows as r (r.label)}
        <span class="dist-seg tone-{r.tone || 'neutral'}" style="flex-grow: {r.value}"></span>
      {/each}
    </div>
    <ul class="dist-legend">
      {#each rows as r (r.label)}
        <li class="dist-item tone-{r.tone || 'neutral'}">
          {#if r.href}
            <a href={r.href}>
              <span class="dist-swatch" aria-hidden="true"></span>
              <span class="dist-label">{r.label}</span>
              <span class="dist-value">{r.value}</span>
              <span class="dist-pct">({pct(r.value)}% of {denom})</span>
            </a>
          {:else}
            <span class="dist-swatch" aria-hidden="true"></span>
            <span class="dist-label">{r.label}</span>
            <span class="dist-value">{r.value}</span>
            <span class="dist-pct">({pct(r.value)}% of {denom})</span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
</figure>

<style>
  .dist { margin: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .dist-cap { display: flex; flex-direction: column; gap: 2px; }
  /* Subsection role, not a private size -- see HorizontalBars. */
  .dist-title { margin: 0; }
  .dist-desc, .dist-scope { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .dist-scope { font-style: italic; }
  .dist-empty { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .dist-bar {
    display: flex; width: 100%; height: 12px; border-radius: var(--radius-xs);
    overflow: hidden; background: var(--c-surface-inset); border: 1px solid var(--c-border);
  }
  .dist-seg { display: block; min-width: 2px; background: var(--tone-c, var(--c-neutral)); transition: flex-grow 200ms ease; }
  .dist-legend { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-4); }
  .dist-item, .dist-item a {
    display: flex; align-items: baseline; gap: var(--sp-2);
    font-size: var(--text-sm); color: var(--c-text-2); text-decoration: none;
  }
  .dist-item a { min-height: var(--touch-min); align-items: center; }
  .dist-item a:hover .dist-label, .dist-item a:focus-visible .dist-label { text-decoration: underline; }
  .dist-swatch {
    width: 10px; height: 10px; border-radius: 2px; flex: none;
    background: var(--tone-c, var(--c-neutral)); border: 1px solid var(--c-border);
  }
  .dist-value { font-weight: 700; color: var(--c-text); font-variant-numeric: tabular-nums; }
  .dist-pct { font-size: var(--text-xs); color: var(--c-text-3); }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-info { --tone-c: var(--c-info); }
  .tone-neutral { --tone-c: var(--c-neutral); }
  @media (prefers-reduced-motion: reduce) {
    .dist-seg { transition: none; }
  }
</style>
