<script>
  // The flagship figure (SPEC 7, Change analysis): "Which consumers does this change
  // break, and does the gate let it through?". The later revision leaves the stack on a
  // dashed (projected) wire, splits into at most five disjoint lanes that sum to the
  // total, and the gate sits under the lane that decides it, wired down from its node.
  // Every count comes from the backend tallies over every consumer (lib/impactFigure.ts),
  // so paging the table never changes the drawing. Each lane's count and "N more" link
  // to the consumers table filtered to that outcome, which lists the highlighted ones
  // first, so every name the chips cut is readable there in full.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import Lane from './Lane.svelte';
  import Ghost from './Ghost.svelte';
  import Stack from './Stack.svelte';
  import AnswerCard from './AnswerCard.svelte';
  import { impactModel, answerText } from '../../lib/impactFigure.ts';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { formatRelative } from '../../lib/dateFormat.ts';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ impact: import('../../lib/api.ts').ProductImpact, autoplay?: boolean, laneHref?: ((outcome: string) => string) | null, onlane?: ((outcome: string, e: MouseEvent) => void) | null }} */
  let { impact, autoplay = false, laneHref = null, onlane = null } = $props();
  const m = $derived(impactModel(impact));
  const tallies = $derived(impact.consumers);
  const asOf = $derived(impact.meta?.asOf ? formatRelative(impact.meta.asOf) : '');
  const gateLine = $derived.by(() => {
    const r = impact.gateReason ?? '';
    return `${m.gate?.says ?? ''}${r ? `: ${r}` : ''}${/[.!?]$/.test(r) ? '' : '.'}`;
  });
</script>

{#snippet swatch(/** @type {string} */ cls)}
  <svg class="swk" width="22" height="14" viewBox="0 0 22 14" aria-hidden="true" focusable="false"><g class="slab {cls}"><path class="top" d="M11 2 L20 7 L11 12 L2 7 Z" /><path class="edge" d="M11 2 L20 7 L11 12 L2 7 Z" /></g></svg>
{/snippet}

{#snippet gateRow()}
  {#if m.gate}
    <div class="lane-gate" data-testid="impact-gate">
      <Node shape="diamond" big tone={m.gate.tone} glyph={m.gate.glyph} a="gate" m="gate" />
      <span class="t-meta">{m.gateWhy}</span>
      <span class="stamp t-{m.gate.tone}">{m.gate.stamp}</span>
    </div>
  {/if}
{/snippet}

<Figure id="impact" testid="impact-figure" question="Which consumers does this change break, and does the gate let it through?"
  alt={m.alt} wires={m.wires} steps={m.steps} {autoplay} autoKey={m.key}>
  {#snippet stage()}
    <div class="impact">
      <!-- The service is named across the whole figure, so a long name wraps in full
           without pushing its stack down or running under a wire. -->
      <a class="stack-name" href={hashForHref(impact.service?.href)}><bdi>{m.service}</bdi></a>
      <div class="stackcell">
        <Stack layers={[{ tone: 'accent', a: 'next', m: 'next' }, { tone: 'neutral', dash: true, a: 'prev', m: 'prev' }]} />
        <div class="stack-labels">
          <span>{@render swatch('t-accent')}<span class="mono">{m.to}</span> later</span>
          <span>{@render swatch('t-neutral dash')}<span class="mono">{m.from}</span> earlier</span>
        </div>
      </div>
      <div class="splitcell"><Node tone="accent" a="split" m="split" /></div>
      <div class="lanes">
        {#each m.lanes as l, k (l.key)}
          {#if l.ghost}
            <Ghost {k} n={l.n} label={l.label} note="Names hidden by your access. Not counted in the {formatCount(m.total)}." />
          {:else}
            <Lane {k} tone={l.tone} hatch={l.hatch} glyph={l.glyph} label={l.label} n={l.n} total={m.total}
              chips={l.chips} open={m.partial} decides={l.decides}
              href={laneHref ? laneHref(l.key) : ''} onpick={onlane ? (e) => onlane(l.key, e) : null} />
            {#if l.key === 'incompatible' && m.refused}
              <p class="lane-sub t-meta"><Node tone="err" glyph="err" />{countOf(m.refused, 'of them refuses', 'of them refuse')} a required guarantee</p>
            {/if}
          {/if}
          {#if k === m.gateLane}{@render gateRow()}{/if}
        {/each}
        {#if m.gateLane < 0}{@render gateRow()}{/if}
      </div>
    </div>
  {/snippet}
  {#snippet foot()}
    <AnswerCard testid="changes-answer" stamps={[m.classification]} hidden={m.hidden} completeness={impact.meta?.completeness} {asOf}
      provenance="Contract diff of the two revisions, projected on the fleet snapshot">
      {#snippet lead()}<p class="ca-answer-line">{answerText(impact)}</p>{/snippet}
      {#snippet facts()}
        <dt>Change</dt>
        <dd><bdi>{m.service}</bdi> <span class="mono">{m.from} → {m.to}</span></dd>
        <dt>Consumers</dt>
        <dd>
          {#if m.partial}At least {formatCount(m.total)}: {formatCount(m.direct)} direct, {formatCount(m.indirect)} indirect. Consumers known only to {m.missing} are not counted.
          {:else}{formatCount(m.total)} in total: {formatCount(m.direct)} direct, {formatCount(m.indirect)} indirect.{/if}
        </dd>
        {#if (tallies.byOwner?.length ?? 0) > 0}
          <dt>Most affected owners</dt>
          <dd data-testid="impact-owners">
            <!-- A short list, one owner a line, each name elided in its middle (C6) when it
                 does not fit; the full name stays in its title. -->
            <ul class="ac-owners">
              {#each tallies.byOwner ?? [] as o (o.owner)}
                <li><bdi class:fit={elidable(o.owner)} title={o.owner}>{o.owner}</bdi><span class="ac-owner-n">{countOf(o.consumers, 'consumer')}{o.incompatible ? `, ${formatCount(o.incompatible)} incompatible` : ''}</span></li>
              {/each}
            </ul>
            {#if tallies.beyondRanking || tallies.noOwner}
              <span class="t-meta">{[tallies.beyondRanking ? `${countOf(tallies.beyondRanking, 'more consumer', 'more consumers')} of other owners` : '', tallies.noOwner ? `${formatCount(tallies.noOwner)} with no identified owner` : ''].filter(Boolean).join('; ')}</span>
            {/if}
          </dd>
        {/if}
        {#if m.gate}
          <!-- The gate is drawn once in the figure (diamond and stamp); the card says why. -->
          <dt>Gate</dt>
          <dd data-testid="changes-gate">{gateLine}</dd>
        {/if}
      {/snippet}
      {#snippet evidence()}
        <span class="snap-id" class:match={impact.snapshotMatch} title="Snapshot {impact.snapshotId}">{impact.snapshotMatch ? 'Current data ✓' : 'Older snapshot'}</span>
      {/snippet}
    </AnswerCard>
  {/snippet}
  {#snippet caption()}
    <b>If this revision is published</b>, {m.caption}{#if m.figRef}{' '}<span class="fig-ref">{m.figRef}</span>{/if}
  {/snippet}
</Figure>
