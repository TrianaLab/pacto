<script>
  // The flagship figure (SPEC 7, Change analysis): "Which consumers does this change
  // break, and does the gate let it through?". The later revision leaves the stack on a
  // dashed (projected) wire, splits into at most five disjoint lanes that sum to the
  // total, and the gate sits under the lane that decides it, wired down from its node.
  // Every count comes from the backend tallies over every consumer (lib/impactFigure.ts),
  // so paging the table never changes the drawing. Each lane's count and "N more" link
  // to the consumers table filtered to that outcome, which lists the highlighted ones
  // first, so every name the chips cut is readable there in full.
  // An event answer ("if redis becomes unavailable, who breaks?") takes the same code
  // path from eventModel: one neutral subject node instead of the revision stack (nothing
  // is published), lanes by effect and a gate that never blocks.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import Lane from './Lane.svelte';
  import Ghost from './Ghost.svelte';
  import Stack from './Stack.svelte';
  import AnswerCard from './AnswerCard.svelte';
  import EntityLink from '../EntityLink.svelte';
  import { impactModel, eventModel, eventPhrase, answerText } from '../../lib/impactFigure.ts';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { formatRelative } from '../../lib/dateFormat.ts';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ impact: import('../../lib/api.ts').ProductImpact, autoplay?: boolean, laneHref?: ((outcome: string) => string) | null, onlane?: ((outcome: string, e: MouseEvent) => void) | null }} */
  let { impact, autoplay = false, laneHref = null, onlane = null } = $props();
  const isEvent = $derived(!!impact.event);
  /** @type {any} */
  const m = $derived(impact.event ? eventModel(impact) : impactModel(impact));
  const EVENT_STAMP = { unavailable: 'Unavailable', removed: 'Removed', regressed: 'Regressed' };
  const provenance = $derived(!isEvent ? 'Contract diff of the two revisions, projected on the fleet snapshot'
    : `Dependents of ${m.service} in the fleet snapshot${impact.event?.kind === 'regressed' ? ', with the delivery evidence you may read' : ''}`);
  const tallies = $derived(impact.consumers);
  // Two owners can share a label (team shop and DRI shop): the ranking groups by
  // canonical key, so a shared label names its namespace.
  const sharedLabel = (label) => (tallies.byOwner ?? []).filter((o) => o.owner === label).length > 1;
  const ownerKind = (key) => (key.startsWith('dri:') ? 'DRI' : 'Team');
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

<Figure id="impact" testid="impact-figure" question={isEvent ? `If ${eventPhrase(impact)}, who breaks?` : 'Which consumers does this change break, and does the gate let it through?'}
  alt={m.alt} wires={m.wires} steps={m.steps} {autoplay} autoKey={m.key}>
  {#snippet stage()}
    <div class="impact">
      <!-- The service is named across the whole figure, so a long name wraps in full
           without pushing its stack down or running under a wire. -->
      <a class="stack-name" href={hashForHref(impact.service?.href)}><bdi>{m.service}</bdi></a>
      <div class="stackcell">
        {#if isEvent}
          <Stack layers={[{ tone: 'neutral', a: 'subject', m: 'subject' }]} />
          <div class="stack-labels"><span>{@render swatch('t-neutral')}<bdi class="ev-verb">{m.verb}</bdi></span></div>
        {:else}
          <Stack layers={[{ tone: 'accent', a: 'next', m: 'next' }, { tone: 'neutral', dash: true, a: 'prev', m: 'prev' }]} />
          <div class="stack-labels">
            <span>{@render swatch('t-accent')}<span class="mono">{m.to}</span> later</span>
            <span>{@render swatch('t-neutral dash')}<span class="mono">{m.from}</span> earlier</span>
          </div>
        {/if}
      </div>
      <div class="splitcell"><Node tone="accent" a="split" m="split" /></div>
      <div class="lanes">
        {#each m.lanes as l, k (l.key)}
          {#if l.key === 'coverage'}
            <Ghost {k} n={null} glyph="unknown" label={l.label} note="Consumers known only to {m.missing} are missing." />
          {:else if l.ghost}
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
    <AnswerCard testid="changes-answer" stamps={isEvent ? [{ label: EVENT_STAMP[impact.event?.kind] ?? 'What if', tone: 'neutral' }] : [m.classification]}
      hidden={m.hidden} completeness={impact.meta?.completeness} {asOf} {provenance}>
      {#snippet lead()}<p class="ca-answer-line">{answerText(impact)}</p>{/snippet}
      {#snippet facts()}
        {#if isEvent}
          <dt>Question</dt>
          <dd>What happens if {eventPhrase(impact)}?</dd>
        {:else}
          <dt>Change</dt>
          <dd><bdi>{m.service}</bdi> <span class="mono">{m.from} → {m.to}</span></dd>
        {/if}
        {#if impact.changedOwner}
          <dt>Owner</dt>
          <dd data-testid="impact-changed-owner"><EntityLink ref={impact.changedOwner} showStatus={false} /></dd>
        {/if}
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
              {#each tallies.byOwner ?? [] as o (o.ownerKey || `label:${o.owner}`)}
                <li><bdi class:fit={elidable(o.owner)} title={o.owner}>{o.owner}</bdi>{#if sharedLabel(o.owner) && o.ownerKey}<span class="t-meta">{` (${ownerKind(o.ownerKey)})`}</span>{/if}<span class="ac-owner-n">{countOf(o.consumers, 'consumer')}{o.incompatible ? `, ${formatCount(o.incompatible)} incompatible` : ''}</span></li>
              {/each}
            </ul>
            {#if tallies.beyondRanking || tallies.noOwner}
              <span class="t-meta">{[tallies.beyondRanking ? `${countOf(tallies.beyondRanking, 'more consumer', 'more consumers')} of other owners` : '', tallies.noOwner ? `${formatCount(tallies.noOwner)} with no identified owner` : ''].filter(Boolean).join('; ')}</span>
            {/if}
          </dd>
        {/if}
        {#if impact.templates?.total}
          <!-- Golden paths run nowhere: listed to review, never counted as consumers. -->
          <dt>Golden paths to review</dt>
          <dd data-testid="impact-templates">
            {#each impact.templates.items ?? [] as t, i (t.key)}{i ? ', ' : ''}<EntityLink ref={t} showStatus={false} />{/each}{impact.templates.truncated ? ` and ${formatCount(impact.templates.total - impact.templates.count)} more` : ''}.
            <span class="t-meta">They run nowhere, so they never hold the gate.</span>
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
    {#if isEvent}<b>{m.lead}</b>: {m.caption}{:else}<b>If this revision is published</b>, {m.caption}{/if}{#if m.figRef}{' '}<span class="fig-ref">{m.figRef}</span>{/if}
  {/snippet}
</Figure>
