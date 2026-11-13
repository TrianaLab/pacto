<script>
  // The Offerings list figure (SPEC 7 Offerings row): "Are the requests for platform
  // offerings ready?". The requests of every matched offering, a neutral population hub,
  // stand on the phase track the offering page draws: where they are now, never how
  // they moved (A6). Every count is the backend's fleet tally (B3). No offering filter
  // lists a phase, so station counts are plain numbers, except Failed, which lists the
  // offerings with a failed request; the offerings with most failures are named.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import HubNode from './HubNode.svelte';
  import ProvenanceStrip from './ProvenanceStrip.svelte';
  import { offeringsPhaseModel } from '../../lib/offeringsFigure.ts';
  import { countOf, formatCount } from '../../lib/format.ts';
  import { fleetEntityUrl } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ aggregate: import('../../lib/offeringsFigure.ts').OfferingsAggregate | null, failedHref: string, meta?: import('../../lib/provenance.ts').MetaLike | null }} */
  let { aggregate, failedHref, meta = null } = $props();
  const m = $derived(offeringsPhaseModel(aggregate));
  const st = (/** @type {string} */ k) => m.stations.find((s) => s.key === k);
</script>

{#snippet count(/** @type {import('../../lib/entityFigures.ts').PhaseStation} */ s)}
  {#if s.key === 'failed' && m.failedLists}
    <a href={failedHref} data-lists={m.failedLists}><b>{formatCount(s.n)}</b> · {s.share}<span class="visually-hidden"> failed: list the {countOf(m.failedLists, 'offering')} with a failed request</span></a>
  {:else}
    <span data-nofilter><b>{formatCount(s.n)}</b> · {s.share}</span>
  {/if}
{/snippet}

{#snippet station(/** @type {import('../../lib/entityFigures.ts').PhaseStation} */ s)}
  <div class="ph station ph-{s.key}">
    <Node tone={s.tone} hatch={s.hatch} glyph={s.glyph} big a="ph-{s.key}" m="ph-{s.key}" />
    <span class="ph-l">
      <b>{s.label}</b>
      {@render count(s)}
      {#if s.consumers}<span class="t-meta" data-nofilter>{countOf(s.consumers, 'consumer')}</span>{/if}
      <span class="t-meta">{s.sub}</span>
    </span>
    {#if s.key === 'provisioning'}<i class="anc" data-a="ph-provisioning-b"></i>{/if}
  </div>
{/snippet}

<Figure id="offerings" testid="offerings-figure" question="Are the requests for platform offerings ready?"
  alt={m.summary} wires={m.wires} rowMin={650} compact={true}>
  {#snippet stage()}
    <div class="ofig ofig--fleet">
      <div class="ofig-hub">
        <HubNode tone="neutral" kind="offering" a="hub" />
        <span class="ofig-hl"><b data-nofilter>{countOf(m.requests, 'request')}</b><span class="t-meta" data-nofilter>across {countOf(m.offerings, 'offering')}</span></span>
      </div>
      {@render station(st('requested'))}
      {@render station(st('provisioning'))}
      {@render station(st('ready'))}
      {@render station(st('deleting'))}
      {@render station(st('failed'))}
      {#if m.failed.length}
        <div class="ph-chips station">
          {#each m.failed as c (c.name)}<a class="badge chip t-err" href={fleetEntityUrl('offering', c.name)} title="{c.name}: {countOf(c.n, 'consumer')} with a failed request"><bdi class:fit={elidable(c.name)}>{c.name}</bdi></a>{/each}
          {#if m.moreFailed > 0}<a href={failedHref} aria-label="{formatCount(m.moreFailed)} more {m.moreFailed === 1 ? 'offering' : 'offerings'} with a failed request">{formatCount(m.moreFailed)} more</a>{/if}
        </div>
      {/if}
      {#if st('unknown').n}
        <div class="ph station ph-unknown">
          <Node tone="neutral" hatch glyph="unknown" a="ph-unknown" m="ph-unknown" />
          <span data-nofilter><b>{formatCount(st('unknown').n)}</b> not reported ({st('unknown').share}): {st('unknown').sub}.</span>
        </div>
      {/if}
    </div>
  {/snippet}
  {#snippet caption()}{m.summary} Phases are what each provider last reported, not a history. <ProvenanceStrip {meta} />{/snippet}
</Figure>
