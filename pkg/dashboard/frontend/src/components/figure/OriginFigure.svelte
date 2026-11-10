<script>
  // The origin figure (SPEC 7 r5: Golden paths list): "Which golden paths created the
  // services in the fleet?". Lanes flow into the fleet's neutral HubNode (a population,
  // S10): the top golden paths by services created, Other paths, the services whose
  // origin resolves to no golden path and those that declare none. The lanes partition
  // the fleet (C14); every lane says how many of its own services are non-compliant. The
  // tally is the backend's over the whole fleet (B5); hidden services are counted in the
  // CountTile and never named. Ambient: no narration.
  import Figure from './Figure.svelte';
  import HubNode from './HubNode.svelte';
  import Lane from './Lane.svelte';
  import CountTile from './CountTile.svelte';
  import ProvenanceStrip from './ProvenanceStrip.svelte';
  import { originModel } from '../../lib/goldenFigures.ts';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { sharePct } from '../../lib/figure.ts';

  /** @type {{ aggregate: import('../../lib/goldenFigures.ts').OriginAggregate | null, hrefs: import('../../lib/goldenFigures.ts').OriginHrefs, meta?: import('../../lib/provenance.ts').MetaLike | null }} */
  let { aggregate, hrefs, meta = null } = $props();
  const m = $derived(originModel(aggregate, hrefs));
</script>

<Figure id="origins" testid="origin-figure" question="Which golden paths created the services in the fleet?" alt={m.alt} wires={m.wires} compact={true}>
  {#snippet stage()}
    <div class="rank rank--in">
      <div class="rank-lanes">
        <p class="rank-h t-meta">Services created from each golden path, and their share of the {formatCount(m.total)}</p>
        <div class="lanes">
          {#each m.lanes as l, k (l.key)}
            <Lane {k} tone="neutral" hatch={l.hatch} glyph={l.glyph} label={l.label} ident={l.ident}
              n={l.n} total={m.total} href={l.href} note={l.note} />
          {/each}
        </div>
      </div>
      <!-- Wires end on the hub's label block, so they never cross its words. -->
      <div class="rank-hub" data-a="hub">
        <HubNode tone="neutral" kind="service" a="hub-n" />
        <span class="rank-hl"><b class="stat" data-nofilter>{formatCount(m.total)}</b><span>{m.total === 1 ? 'service' : 'services'}</span><span class="t-meta">in the fleet</span></span>
      </div>
    </div>
  {/snippet}
  <!-- In the foot, not an aside: the lanes need the full width for their links. -->
  {#snippet foot()}
    <CountTile more={m.more} href={hrefs.paths} hidden={m.hidden}
      parts={[`${countOf(m.paths, 'golden path')} created at least one`, `${sharePct(m.created, m.total)} of the fleet from a golden path`]} />
  {/snippet}
  {#snippet caption()}
    Ranked by services created. A service counts under the golden path its contract records as its origin.{#if m.hidden}{' '}{countOf(m.hidden, 'service')} created from a golden path {m.hidden === 1 ? 'is' : 'are'} hidden by your access and in no count.{/if}
    <ProvenanceStrip {meta} />
  {/snippet}
</Figure>
