<script>
  // The trust figures (SPEC 7 r5): Overview, "Is the fleet okay, and how sure are we?",
  // counts sources by state; Sources, "Which sources can we trust right now?", names
  // the ones that are not current. Either way each row is wired into a Bus in its
  // state's style (current solid, stale and partial dotted, unavailable broken with its
  // ✕) and the bus into the snapshot (an accent HubNode). Passive narration, only when a
  // source is unavailable or stale: the unavailable marks appear, then the stale wires
  // carry the one signal (A2, A6).
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import HubNode from './HubNode.svelte';
  import Bus from './Bus.svelte';
  import { trustModel, sourcesModel, SOURCE_STATE } from '../../lib/fleetFigures.ts';
  import { formatCount } from '../../lib/format.ts';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ mode: 'overview' | 'sources', counts: import('../../lib/fleetFigures.ts').SourceCountsLike | null, worst?: import('../../lib/fleetFigures.ts').NamedSource[], asOf?: string, stateHref: (state: string) => string, aside?: import('svelte').Snippet | null }} */
  let { mode, counts, worst = [], asOf = '', stateHref, aside = null } = $props();
  const ov = $derived(trustModel(counts));
  const sm = $derived(sourcesModel(worst, counts));
  const m = $derived(mode === 'overview' ? ov : sm);
  const plural = (n) => (n === 1 ? 'source' : 'sources');
</script>

<Figure id={mode === 'overview' ? 'trust' : 'sources'} testid="trust-figure"
  question={mode === 'overview' ? 'Is the fleet okay, and how sure are we?' : 'Which sources can we trust right now?'}
  alt={m.alt} wires={m.wires} steps={m.steps} autoplay={m.steps.length > 0} autoKey={m.key} compact={mode === 'sources'} {aside}>
  {#snippet stage()}
    <div class="trust">
      <ul class="srcs">
        {#if mode === 'overview'}
          {#each ov.rows as r (r.st)}
            <li class="src stat">
              <a class="src-n" href={stateHref(r.st)}>{formatCount(r.n)}</a>
              <span class="src-t"><span>{plural(r.n)} {SOURCE_STATE[r.st].word}</span><span class="t-meta">{SOURCE_STATE[r.st].sub}</span></span>
              <span class="src-lead" aria-hidden="true"></span>
              <Node shape="square" tone={r.n ? SOURCE_STATE[r.st].tone : 'neutral'} glyph={r.n ? SOURCE_STATE[r.st].glyph : ''} a="s{r.k}" m="s{r.k}" />
            </li>
          {/each}
        {:else}
          {#each sm.rows as r (r.k)}
            <li class="src">
              {#if r.named}
                <span class="src-t">
                  <a class="id" href={hashForHref(r.named.href)} title={r.named.label}><bdi class:fit={elidable(r.named.label)}>{r.named.label}</bdi></a>
                  <span class="t-meta">{r.named.secondary ? `${r.named.secondary} · ` : ''}{SOURCE_STATE[r.st].word}</span>
                </span>
              {:else}
                <span class="src-t">
                  <a href={stateHref(r.st)}>{sm.tallyLabel(r)}</a>
                  <span class="t-meta">{r.st === 'available' ? 'all current' : 'not named here'}</span>
                </span>
              {/if}
              <Node shape="square" tone={SOURCE_STATE[r.st].tone} glyph={SOURCE_STATE[r.st].glyph} a="s{r.k}" m="s{r.k}" />
            </li>
          {/each}
        {/if}
      </ul>
      <Bus n={m.rows.length} />
      <div class="trust-hub">
        <HubNode kind="snapshot" a="snap" />
        <span class="trust-hl"><b>Snapshot</b>{#if asOf}<span class="t-meta">as of {asOf}</span>{/if}<span class="badge wrap" class:t-unk={m.partial}>{m.partial ? 'Partial' : 'Complete'}</span></span>
      </div>
    </div>
  {/snippet}
  {#snippet caption()}
    {m.partial ? 'Knowledge is partial, so a missing result means unknown, never compliant.' : 'Every source is current, so the snapshot is complete.'}
    {' '}<span class="fig-ref">Solid wire: reported. Dotted: stale or partial. Broken: unavailable.</span>
  {/snippet}
</Figure>
