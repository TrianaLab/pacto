<script>
  // The flagship figure (SPEC 7, Change analysis): "Which consumers does this change
  // break, and does the gate let it through?". The later revision leaves the stack on a
  // dashed (projected) wire, splits into at most five disjoint lanes that sum to the
  // total, and the gate sits under the lane that decides it, wired down from its node.
  // Every count comes from the backend tallies over every consumer (lib/impactFigure.ts),
  // so paging the table never changes the drawing; the table below holds every name.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import Lane from './Lane.svelte';
  import Stack from './Stack.svelte';
  import AnswerCard from './AnswerCard.svelte';
  import { impactModel, answerText } from '../../lib/impactFigure.ts';
  import { completenessClass, completenessLabel, formatCount, countOf } from '../../lib/format.ts';
  import { formatRelative } from '../../lib/dateFormat.ts';
  import { hashForHref } from '../../lib/router.ts';

  /** @type {{ impact: import('../../lib/api.ts').ProductImpact, autoplay?: boolean, onmore?: (() => void) | null }} */
  let { impact, autoplay = false, onmore = null } = $props();
  const m = $derived(impactModel(impact));
  const tallies = $derived(impact.consumers);
  const asOf = $derived(impact.meta?.asOf ? formatRelative(impact.meta.asOf) : '');
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
          <Lane {k} tone={l.tone} hatch={l.hatch} glyph={l.glyph} label={l.label} n={l.n} total={l.ghost ? 0 : m.total}
            chips={l.chips} ghost={l.ghost} open={m.partial && !l.ghost} decides={l.decides} onmore={l.ghost ? null : onmore} />
          {#if l.ghost}
            <p class="lane-sub t-meta">Names hidden by your access. Not counted in the {formatCount(m.total)}.</p>
          {:else if l.key === 'incompatible' && m.refused}
            <p class="lane-sub t-meta"><Node tone="err" glyph="warn" />{countOf(m.refused, 'of them refuses', 'of them refuse')} a required guarantee</p>
          {/if}
          {#if k === m.gateLane}{@render gateRow()}{/if}
        {/each}
        {#if m.gateLane < 0}{@render gateRow()}{/if}
      </div>
    </div>
  {/snippet}
  {#snippet foot()}
    <AnswerCard testid="changes-answer" stamps={[m.classification, ...(m.gate ? [{ label: m.gate.stamp, tone: m.gate.tone }] : [])]}>
      <p class="ca-answer-line">{answerText(impact)}</p>
      <dl class="facts">
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
            {#each tallies.byOwner ?? [] as o, i (o.owner)}{i ? ', ' : ''}<bdi>{o.owner}</bdi> ({formatCount(o.consumers)}{o.incompatible ? `, ${formatCount(o.incompatible)} incompatible` : ''}){/each}{#if tallies.beyondRanking}; {countOf(tallies.beyondRanking, 'more consumer', 'more consumers')} of other owners{/if}{#if tallies.noOwner}; {formatCount(tallies.noOwner)} with no identified owner{/if}
          </dd>
        {/if}
        {#if m.hidden}
          <dt>You may not see</dt>
          <dd>{formatCount(m.hidden)}, not counted and never named</dd>
        {/if}
        {#if m.gate}
          <dt>Release gate</dt>
          <dd data-testid="changes-gate"><span class="badge {m.gate.cls}">{m.gate.label}</span> {impact.gateReason}</dd>
        {/if}
        <dt>Evidence</dt>
        <dd>
          <span class="badge wrap {completenessClass(impact.meta?.completeness)}">{completenessLabel(impact.meta?.completeness)}</span>
          <span class="snap-id" class:match={impact.snapshotMatch} title="Snapshot {impact.snapshotId}">{impact.snapshotMatch ? 'Current data ✓' : 'Older snapshot'}</span>
        </dd>
        {#if asOf}<dt>As of</dt><dd>{asOf}</dd>{/if}
        <dt>Provenance</dt>
        <dd>Contract diff of the two revisions, projected on the fleet snapshot</dd>
      </dl>
    </AnswerCard>
  {/snippet}
  {#snippet caption()}
    <b>If this revision is published</b>, {m.caption}{#if m.figRef}{' '}<span class="fig-ref">{m.figRef}</span>{/if}
  {/snippet}
</Figure>
