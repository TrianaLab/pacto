<script>
  // The service Environments figure (SPEC 7, SPEC 21): "Where is X on its way to
  // production?", drawn from that service's own promotion graph. Columns are the links
  // from an entry; a fork stacks its branches in one column. An entry shows where its
  // candidates come from; a source Pacto cannot follow is a hatched marker with the
  // reason and no wire. Each Stage is a neutral station (S10) with its environment, the
  // version it runs and its state. Ambient: no narration (no promotion time is reported
  // here, so a replay would assert a transition the data does not hold).
  import Figure from './Figure.svelte';
  import Station from './Station.svelte';
  import Node from './Node.svelte';
  import { promotionModel } from '../../lib/envFigure.ts';
  import { GLYPH_PATH } from '../../lib/figure.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ service: string, graph: NonNullable<import('../../lib/api.ts').DeliveryEnvironments['promotion']>, rows: import('../../lib/api.ts').DeliveryEnvironments['environments'], id?: string }} */
  let { service, graph, rows, id = 'promo' } = $props();
  const m = $derived(promotionModel(service, graph, rows));
</script>

<Figure {id} testid="promotion-figure" question={`Where is ${service} on its way to production?`}
  alt={m.alt} wires={m.wires} rowMin={m.cols * 170}>
  {#snippet stage()}
    <div class="pgraph" style:--cols={m.cols} style:--rows={m.rows}>
      {#each m.stations as s, i (s.key)}
        {#if s.source}
          <div class="pg-src" class:pg-unk={s.source.kind === 'unknown'} style:grid-column={1} style:grid-row={s.row}>
            {#if s.source.kind === 'unknown'}
              <Node tone="neutral" hatch glyph="unknown" />
            {:else}
              <Node tone="neutral" shape={s.source.kind === 'artifact' ? 'circle' : 'square'} a="src{i}" m="src{i}" />
            {/if}
            <span class="pg-l"><b class="id" title={s.source.label}><bdi class:fit={elidable(s.source.label)}>{s.source.label}</bdi></b><span class="t-meta">{s.source.sub}</span></span>
          </div>
        {/if}
        <div class="pg-st station" style:grid-column={s.col} style:grid-row={s.row} data-testid="promotion-station">
          <Station a="s{i}" m="s{i}" w={96} />
          <span class="pg-l">
            <b class="id" title={s.env}><bdi class:fit={elidable(s.env)}>{s.env}</bdi></b>
            {#if s.stage}<span class="t-meta id" title={s.stage}><bdi class:fit={elidable(s.stage)}>{s.stage}</bdi></span>{/if}
            {#if s.state}
              <span class="pg-run">
                <svg class="qglyph t-{s.state.tone}" class:unk={s.state.hatch} width="16" height="16" viewBox="-8 -8 16 16" aria-hidden="true" focusable="false"><circle r="7" /><path d={GLYPH_PATH[s.state.glyph]} /></svg>
                <span class="mono">{s.version || 'nothing yet'}</span>
                {#if s.state.stamp}<span class="stamp t-{s.state.tone}">{s.state.stamp}</span>{:else}<span class="t-meta">{s.state.word}</span>{/if}
              </span>
            {:else}
              <span class="t-meta">not on this page of the board</span>
            {/if}
            {#if s.cycle}<span class="t-meta">on a cycle of upstream links</span>{/if}
          </span>
        </div>
      {/each}
    </div>
  {/snippet}
  {#snippet caption()}{m.caption}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>
