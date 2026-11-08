<script>
  // The rank figure (SPEC 7 r5: Owners, Domains): "Largest owners by non-compliant
  // services". A neutral HubNode (the non-compliant population, never a status tone,
  // S10) fanned into at most five lanes that partition it (C14): the top groups by count,
  // Other, then the services no group can name. Group nodes stay neutral; the share bars
  // carry the err tone. Every count links to the services it counts (T3); Other's note
  // links to the table sorted the same way. The ranking is the backend's over the whole
  // population (B8). Ambient: no narration.
  import Figure from './Figure.svelte';
  import HubNode from './HubNode.svelte';
  import Lane from './Lane.svelte';
  import { rankModel } from '../../lib/fleetFigures.ts';
  import { formatCount, countOf } from '../../lib/format.ts';

  /** @type {{ kind: 'owner' | 'domain', aggregate: import('../../lib/fleetFigures.ts').RankAggregate | null, hrefs: import('../../lib/fleetFigures.ts').RankHrefs }} */
  let { kind, aggregate, hrefs } = $props();
  const m = $derived(rankModel(kind, aggregate, hrefs));
</script>

<Figure id="rank" testid="rank-figure" question="Largest {m.word}s by non-compliant services" alt={m.alt} wires={m.wires} compact={true}>
  {#snippet stage()}
    <div class="rank">
      <!-- Wires leave the hub's label block, so they never cross its words. -->
      <div class="rank-hub" data-a="hub">
        <HubNode tone="neutral" kind={kind} a="hub-n" />
        <span class="rank-hl"><b class="stat" data-nofilter>{formatCount(m.total)}</b><span>non-compliant</span><span class="t-meta">of {countOf(m.services, 'service')}</span></span>
      </div>
      <div class="rank-lanes">
        <p class="rank-h t-meta">Non-compliant services, and their share of the {formatCount(m.total)}</p>
        <div class="lanes">
          {#each m.lanes as l, k (l.key)}
            <Lane {k} tone="neutral" barTone="err" hatch={l.hatch} glyph={l.glyph} label={l.label} ident={l.ident}
              n={l.n} total={m.total} href={l.href} note={l.note} />
          {/each}
        </div>
      </div>
    </div>
  {/snippet}
  {#snippet caption()}
    Ranked by count, so large {m.word}s rank high.{#if m.rate}{' '}The highest rate is <bdi>{m.rate.label}</bdi>: {m.rate.text}.{/if}
  {/snippet}
</Figure>
