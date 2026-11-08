<script>
  // The source page's figure (SPEC 7): "What does this source report, and is it
  // current?". One source wired to the snapshot in its state's style: solid when current,
  // dotted when stale or partial, broken when unavailable (an unavailable source reports
  // nothing in this snapshot, so what it holds is unknown).
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import HubNode from './HubNode.svelte';
  import { sourceModel } from '../../lib/entityFigures.ts';
  import { countOf } from '../../lib/format.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ source: any, name: string, asOf?: string }} */
  let { source, name, asOf = '' } = $props();
  const m = $derived(sourceModel(source.health));
  const reports = $derived(source.health === 'unavailable'
    ? 'Nothing in this snapshot; what it holds is unknown'
    : `${countOf(source.revisionCount ?? 0, 'contract revision')} and ${countOf(source.targetCount ?? 0, 'operational target')}`);
</script>

<Figure id="source" testid="source-figure" question="What does this source report, and is it current?"
  alt={`${name}: ${m.words}. ${reports}.`} wires={m.wires}>
  {#snippet stage()}
    <div class="srcfig">
      <div class="srcfig-s">
        <span class="id" title={name}><bdi class:fit={elidable(name)}>{name}</bdi></span>
        <span class="t-meta">{source.kind ? `${source.kind} · ` : ''}{m.words}</span>
        <Node shape="square" tone={m.tone} hatch={m.hatch} glyph={m.glyph} a="src" m="src" />
      </div>
      <div class="srcfig-h"><HubNode kind="snapshot" a="snap" /><span class="srcfig-hl"><b>Snapshot</b>{#if asOf}<span class="t-meta">as of {asOf}</span>{/if}</span></div>
    </div>
  {/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Reports</dt><dd data-nofilter>{reports}</dd>
    </dl>
  {/snippet}
  {#snippet caption()}
    {source.health === 'available' ? 'This source is current.' : source.health === 'stale' ? 'This source is stale, so what it reports may be out of date.' : source.health === 'unavailable' ? 'This source is unavailable, so everything it would report is unknown.' : 'This source answered only in part, so some of what it holds is unknown.'}
    {' '}<span class="fig-ref">Solid wire: reported. Dotted: stale or partial. Broken: unavailable.</span>
  {/snippet}
</Figure>
