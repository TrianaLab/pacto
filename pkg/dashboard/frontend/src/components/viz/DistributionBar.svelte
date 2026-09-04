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
  //   * the growing segment is marked `data-motion`, so the product-wide reduced-motion
  //     policy in styles/tokens.css drops its transition and e2e/viz-acceptance.spec.ts
  //     proves it did. This component states no motion policy of its own.
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
  // The limit of that last case is total == 0 with buckets that counted something.
  // The denominator still does not move — 0 is an authoritative answer, and the
  // contradiction is the point — but a SHARE of an empty population does not exist.
  // Printing "3 (0% of 0)" states a measurement: it says three of nothing is nought
  // percent, which is not a smaller version of the truth but a different claim. So
  // the counts stay, the over-count stays, and the percentage is withdrawn and said
  // to be unavailable rather than rendered as a plausible zero.
  //
  // The inconsistency notice is TEXT, not a colour: this component's whole contract
  // is that nothing is conveyed by colour or shape alone.
  //
  // `selected` is the LABEL of the bucket the page is currently filtered to, so the
  // bar answers "which slice am I looking at". It is a label rather than a wire value
  // because a segment carries no wire value; callers holding one read the label off
  // the bucket table with `bucketLabel(STATES, value)`.
  let {
    title = '',
    level = 3,
    description = '',
    scopeNote = '',
    segments = [],
    total = null,
    selected = '',
    emptyLabel = 'Nothing to show yet.',
  } = $props();

  // Which row the pointer or the keyboard is on. A slice and its legend entry are two
  // halves of one thing, and on a four-way bar of similar tones the reader could not
  // tell which was which without counting. Hovering either dims the others.
  //
  // Purely presentational, so it stays out of the accessible tree: the legend already
  // names every bucket, and a screen reader gets no dimming to interpret.
  let active = $state('');
  const dimmed = (label) => (active !== '' && active !== label) || (active === '' && selected !== '' && selected !== label);

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
  // An authoritative population of zero that the buckets nonetheless counted against.
  // Distinct from "nothing to show": there IS something on screen, and it contradicts
  // the denominator.
  const emptyPopulation = $derived(denom === 0 && rows.length > 0);
  // Only ever a companion to the exact count, never a replacement. With no population
  // there is no share to be a companion to, so the row says so.
  //
  // A decimal below a hundred is false precision: with 8 services, "12.5%" states the
  // share to a tenth of a percent when the smallest step the data can take is 12.5
  // points. Worse, it invites the reader to compare 12.5 against 12.4 on a population
  // where no such difference exists. Above a hundred the tenth is a real distinction,
  // so it stays.
  //
  // The same rounding runs out at the other end. A single invalid target in a fleet of
  // three thousand is 0.033%, which one decimal rounds to a flat zero -- so the row reads
  // "Invalid 1 (0% of 3000)" and contradicts the count printed beside it. That row is
  // exactly the one a triage page exists to surface, so a non-zero share never prints as
  // nought: it prints as the bound it is under.
  const pct = (v) => (denom >= 100 ? Math.round((v / denom) * 1000) / 10 : Math.round((v / denom) * 100));
  const pctText = (v) => {
    const p = pct(v);
    return p === 0 && v > 0 ? `<${denom >= 100 ? '0.1' : '1'}` : `${p}`;
  };
  const pctLabel = (v) => (denom > 0 ? `(${pctText(v)}% of ${denom})` : '(share unavailable)');

  // A countable population is drawn as individuals rather than as proportions. Nine
  // targets on a proportional bar is a shape the reader has to convert back into nine
  // things; drawn as nine marks they can see that two of them are red without reading a
  // number. Above the ceiling the marks stop being countable and the proportion is the
  // honest reading again, so the bar comes back.
  //
  // Gated on `over === 0` because an over-count has no individuals to draw: the buckets
  // account for more things than exist, and a mark per counted thing would render that
  // contradiction as a larger, complete-looking population.
  const UNIT_MAX = 120;
  const unit = $derived(
    denom > 0 && denom <= UNIT_MAX && over === 0 && rows.every((r) => Number.isInteger(r.value)),
  );
  // `rows` sums to exactly `denom` whenever there is no over-count -- the shortfall is
  // already carried as the Unclassified row -- so the marks partition the population by
  // construction and no cell is invented or dropped.
  const cells = $derived(unit ? rows.flatMap((r) => Array.from({ length: r.value }, () => r)) : []);
  // The mark is sized from the population so a small one is BIG. Nine targets drawn as
  // nine 16px squares is a 150px smudge in a 400px column -- technically countable and
  // visually nothing, which is the state this component was in when the marks first
  // landed. Nine at 26px is a block the eye lands on from across the room, and the
  // ladder steps down only as fast as it has to for the row to still fit beside its
  // siblings. It is a ladder rather than a container query because the sizes are chosen
  // against the two column widths this product actually uses, and a fluid mark would
  // make the same population a different size on every page that draws it.
  const unitPx = $derived(denom <= 12 ? 26 : denom <= 20 ? 18 : denom <= 60 ? 12 : 8);
</script>

<figure class="dist">
  <!-- Title only. The description used to live in here too, which put two lines of grey
       prose between the heading and the picture -- so a page of five distributions was
       read as ten lines of caveat with graphics between them, and the figure's accessible
       name was the whole paragraph. The prose is a footnote to the drawing, so it is
       printed after it. -->
  <figcaption class="dist-cap">
    <svelte:element this={`h${level}`} class="dist-title t-subsection-title">{title}</svelte:element>
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
        being counted twice or against the wrong population,
        {#if emptyPopulation}
          and there is no population to take a share of, so the counts below are shown
          without percentages rather than as shares of zero.
        {:else}
          so read the percentages as suspect: they are shares of {denom}, and they
          total more than 100%.
        {/if}
      </p>
    {/if}
    <!-- One element either way, so the graphic is hidden from assistive technology in
         exactly one place and neither drawing can be added to the accessible tree by
         forgetting to. No role on the marks, deliberately: they live inside
         aria-hidden="true" and are not in the accessible tree at all, so giving them one
         would put a decorative shape back into it. The pointer gesture they carry is
         presentational, and its keyboard equivalent is the legend entry below, which IS
         focusable. -->
    <div
      class="dist-bar"
      class:dist-bar-warn={over > 0}
      class:dist-units={unit}
      style={unit ? `--unit: ${unitPx}px` : undefined}
      aria-hidden="true"
    >
      {#if unit}
        {#each cells as c, i (i)}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <span
            class="dist-unit tone-{c.tone || 'neutral'}"
            class:dim={dimmed(c.label)}
            class:sel={selected === c.label}
            data-motion
            onmouseenter={() => (active = c.label)}
            onmouseleave={() => (active = '')}
          ></span>
        {/each}
      {:else}
        {#each rows as r (r.label)}
          <!-- svelte-ignore a11y_no_static_element_interactions -->
          <span
            class="dist-seg tone-{r.tone || 'neutral'}"
            class:dim={dimmed(r.label)}
            class:sel={selected === r.label}
            data-motion
            style="flex-grow: {r.value}"
            onmouseenter={() => (active = r.label)}
            onmouseleave={() => (active = '')}
          ></span>
        {/each}
      {/if}
    </div>
    <ul class="dist-legend">
      {#each rows as r (r.label)}
        <li
          class="dist-item tone-{r.tone || 'neutral'}"
          class:dim={dimmed(r.label)}
          onmouseenter={() => (active = r.label)}
          onmouseleave={() => (active = '')}
          onfocusin={() => (active = r.label)}
          onfocusout={() => (active = '')}
        >
          {#if r.href}
            <a href={r.href} aria-current={selected === r.label ? 'true' : undefined}>
              <span class="dist-swatch" aria-hidden="true"></span>
              <span class="dist-label">{r.label}</span>
              <span class="dist-value">{r.value}</span>
              <span class="dist-pct">{pctLabel(r.value)}</span>
            </a>
          {:else}
            <span class="dist-swatch" aria-hidden="true"></span>
            <span class="dist-label">{r.label}</span>
            <span class="dist-value">{r.value}</span>
            <span class="dist-pct">{pctLabel(r.value)}</span>
          {/if}
        </li>
      {/each}
    </ul>
  {/if}
  {#if description || scopeNote}
    <div class="dist-note">
      {#if description}<p class="dist-desc">{description}</p>{/if}
      {#if scopeNote}<p class="dist-scope">{scopeNote}</p>{/if}
    </div>
  {/if}
</figure>

<style>
  .dist { margin: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .dist-cap { display: flex; flex-direction: column; gap: 2px; }
  /* Subsection role, not a private size -- see HorizontalBars. */
  .dist-title { margin: 0; }
  /* Pushed to the bottom of the figure and off the reading path to the numbers: the
     margin-top: auto matters in the grids that stretch these figures to a common height,
     where it keeps the footnote on the floor of the cell instead of floating under a
     short legend. */
  .dist-note { margin-top: auto; padding-top: var(--sp-1); display: flex; flex-direction: column; gap: 2px; }
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
  /* The 2px gap is a boundary, not decoration: two adjacent segments of similar tone
     (Unknown beside Warning, both amber) read as one wide slice without it, and the
     reader counts three buckets on a four-bucket bar. */
  .dist-bar {
    display: flex; gap: 2px; width: 100%; height: 12px; border-radius: var(--radius-xs);
    overflow: hidden; background: var(--c-surface-inset); border: 1px solid var(--c-border);
  }
  /* A bar that fills edge to edge reads as a complete distribution. When the buckets
     over-count it is not one, so it is drawn hatched — the notice above already says
     so in words; this only stops the shape from contradicting it. */
  .dist-bar-warn { border-color: var(--c-warn); border-style: dashed; }
  /* Marks, not a bar: the frame and the fixed height belong to a single continuous shape
     and would read as a container the marks sit inside. They size themselves and wrap. */
  .dist-units {
    height: auto; flex-wrap: wrap; overflow: visible;
    background: none; border: 0; border-radius: 0;
    /* One rule instead of a second size ladder: the gutter is a fixed fraction of the
       mark, so the marks read as a field of individuals at every step and never as a
       row of bars with a hairline between them. */
    gap: max(2px, calc(var(--unit) / 6));
  }
  .dist-unit {
    display: block; width: var(--unit); height: var(--unit); flex: none;
    border-radius: max(2px, calc(var(--unit) / 8));
    background: var(--tone-c, var(--c-neutral));
    transition: opacity var(--motion-feedback);
  }
  .dist-unit.dim { opacity: 0.25; }
  .dist-unit.sel { box-shadow: inset 0 0 0 2px var(--c-text); }
  .dist-seg {
    display: block; min-width: 2px; background: var(--tone-c, var(--c-neutral));
    transition: flex-grow var(--motion-reveal), opacity var(--motion-feedback);
  }
  /* The reader caused this, so it is the feedback role and not an entrance. Dimming the
     OTHERS rather than brightening the one keeps every slice its own tone: brightening
     would mean two renderings of the same colour on one bar. */
  .dist-seg.dim { opacity: 0.3; }
  /* The filtered slice, marked while the pointer is elsewhere. An inset outline rather
     than a tone change, so the swatch still reads as its own bucket -- and the legend
     link carries aria-current, so the mark is never the only statement of it. */
  .dist-seg.sel { box-shadow: inset 0 0 0 2px var(--c-text); }
  .dist-item { transition: opacity var(--motion-feedback); }
  .dist-item.dim { opacity: 0.45; }
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
</style>
