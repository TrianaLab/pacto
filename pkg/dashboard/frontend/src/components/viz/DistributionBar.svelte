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
  // preview would silently state a fleet-wide proportion it cannot know.
  //
  // So when `total` is given it IS the denominator, in both directions:
  //
  //   sum < total  the shortfall is drawn as an explicit Unclassified slice, never
  //                absorbed into the last bucket;
  //   sum == total  the ordinary case;
  //   sum > total  the buckets classify more than exists, which is contradictory
  //                data. Raising the denominator to the bucket sum would make it
  //                disappear: the remainder would vanish, every slice would sum to a
  //                clean 100%, and the page would present an impossible distribution
  //                as a complete one. The denominator stays put, the percentages go
  //                over 100 where they are, and the contradiction is stated in
  //                words — because a reader acting on "100% classified" when eight
  //                services were classified ten times is acting on nothing.
  //
  // The inconsistency notice is TEXT, not a colour: this component's whole contract
  // is that nothing is conveyed by colour or shape alone.
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
  const authoritative = $derived(typeof total === 'number' && total >= 0);
  const denom = $derived(authoritative ? total : sum);
  const rest = $derived(Math.max(0, denom - sum));
  // Over-classified: more counted than the population being counted. Never rendered
  // away by widening the denominator.
  const over = $derived(authoritative ? Math.max(0, sum - total) : 0);
  // Empty buckets are dropped here and KEPT in HorizontalBars, and the difference is the
  // difference between the two questions. These rows PARTITION a stated denominator, so
  // the rows that remain always reconcile to it and an empty bucket adds a legend entry
  // beside a slice of zero width -- four of them on a posture bar, saying nothing. A
  // ranked comparison has no denominator to reconcile against, so there a zero is the
  // answer ("nothing is running for this owner") and dropping it deletes information.
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
    {#if over > 0}
      <!-- Stated before the bar, not after it: by the time a reader has read the
           slices they have already believed them. `role="status"` so it is announced
           rather than only seen, and the numbers are spelled out so the notice does
           not depend on being read next to the legend. -->
      <p class="dist-warn" role="status" data-testid="dist-inconsistent">
        <strong>These numbers do not add up.</strong>
        The buckets below account for {sum} across a population of {denom}
        — {over} more than {denom === 1 ? 'there is' : 'there are'}. Something is
        being counted twice or against the wrong population, so read the percentages
        as suspect: they are shares of {denom}, and they total more than 100%.
      </p>
    {/if}
    <div class="dist-bar" class:dist-bar-warn={over > 0} aria-hidden="true">
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
  /* The notice carries the whole message on its own; the border and the hatching
     below are redundant reinforcement, never the signal. */
  .dist-warn {
    margin: 0; font-size: var(--text-sm); color: var(--c-text);
    border: 1px solid var(--c-warn); border-left-width: 3px;
    border-radius: var(--radius-xs); padding: var(--sp-2) var(--sp-3);
    background: var(--c-surface-inset);
  }
  .dist-bar {
    display: flex; width: 100%; height: 12px; border-radius: var(--radius-xs);
    overflow: hidden; background: var(--c-surface-inset); border: 1px solid var(--c-border);
  }
  /* A bar that fills edge to edge reads as a complete distribution. When the buckets
     over-count it is not one, so it is drawn hatched — the notice above already says
     so in words; this only stops the shape from contradicting it. */
  .dist-bar-warn { border-color: var(--c-warn); border-style: dashed; }
  .dist-seg { display: block; min-width: 2px; background: var(--tone-c, var(--c-neutral)); transition: flex-grow 200ms ease; }
  .dist-bar-warn .dist-seg { opacity: 0.55; }
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
