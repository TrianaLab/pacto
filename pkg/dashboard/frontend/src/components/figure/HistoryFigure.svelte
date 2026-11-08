<script>
  // The Delivery History figure (SPEC 7): "What happened in the last 24 hours, and where
  // is the record missing?". Twelve equal columns stacked by kind, worst on top (one
  // mark each, so at most 12 marks), a count over each that opens that slice's entries,
  // and a dashed axis with ⊗ under a column whose record is missing (never a hatch:
  // the hatch is "unknown, accepted"). Ambient: no narration.
  import Figure from './Figure.svelte';
  import { historyModel, TL_KINDS, TL_HEIGHT } from '../../lib/historyFigure.ts';
  import { formatCount } from '../../lib/format.ts';

  /** @type {{ timeline: import('../../lib/generated/schema.ts').components['schemas']['Delivery.HistoryTimeline'], href: (since: string, until: string) => string, source: 'records' | 'status' | 'cluster', live: boolean, question?: string }} */
  let { timeline, href, source, live, question = 'What happened in the last 24 hours, and where is the record missing?' } = $props();
  const m = $derived(historyModel(timeline, { source, live }));
</script>

<Figure id="hist" testid="history-figure" {question} alt={m.alt} compact={true}>
  {#snippet stage()}
    <div class="tl-wrap">
      <ol class="tl" style:--tl-h="{TL_HEIGHT}px">
        {#each m.columns as c (c.i)}
          <li class="tl-c" class:gap={c.missing > 0}>
            {#if c.n}<a class="tl-n" href={href(c.start, c.end)}>{formatCount(c.n)}</a>{:else}<span class="tl-n" data-nofilter>0</span>{/if}
            <span class="tl-bar mk" data-m="tl{c.i}" aria-hidden="true">{#each c.segs as s (s.k.key)}<i class="t-{s.k.tone}" class:hatch={s.k.hatch} style:height="{s.h}px"></i>{/each}</span>
            <span class="tl-ax" aria-hidden="true">{#if c.missing}<svg class="tl-x" width="14" height="14" viewBox="-7 -7 14 14" focusable="false"><circle r="5.5" /><path d="M-2.5 -2.5 L2.5 2.5 M2.5 -2.5 L-2.5 2.5" /></svg>{/if}</span>
          </li>
        {/each}
      </ol>
      <div class="tl-axis t-meta" aria-hidden="true"><span>{m.ticks.first}</span><span class="tl-mid">{m.ticks.mid}</span><span>{m.ticks.last}</span></div>
    </div>
  {/snippet}
  {#snippet aside()}
    <dl class="facts"><dt>Window</dt><dd>{m.window} UTC</dd><dt>Changes</dt><dd><span data-nofilter>{formatCount(m.total)}</span></dd></dl>
    <ul class="keyl">
      {#each TL_KINDS as k (k.key)}
        <li><i class="sw t-{k.tone}" class:hatch={k.hatch}></i><span>{k.label}</span><b data-nofilter>{formatCount(timeline.totals[k.key])}</b></li>
      {/each}
      <li>
        <svg class="gapk" width="14" height="18" viewBox="0 0 14 18" aria-hidden="true" focusable="false"><path d="M0 4 H14" /><circle cx="7" cy="12" r="5" /><path d="M5 10 L9 14 M9 10 L5 14" /></svg>
        <span>Record missing</span>
        {#if m.missingKnown}<b data-nofilter>{formatCount(timeline.totals.missing)}</b>{:else}<span class="t-meta">{source === 'cluster' ? "shown on one Stage's history" : 'needs signed records'}</span>{/if}
      </li>
    </ul>
    {#if m.gapWords.length}<p class="t-meta tl-gaps">Missing: {m.gapWords.join('; ')}.</p>{/if}
  {/snippet}
  {#snippet caption()}{m.caption}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>
