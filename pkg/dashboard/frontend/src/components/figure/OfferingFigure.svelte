<script>
  // The offering page's figure (SPEC 7): "Who provides this, who uses it, and is their
  // request ready?". Providers fan into the offering (two marks at most, the rest a "+N"
  // count that links to the providers list), and the offering's requests stand on the
  // phase track: where they are now, never how they moved (A6). Every count comes from
  // the backend tallies over every use (B3); the consumers that failed are named by the
  // backend, never picked from the loaded page.
  import Figure from './Figure.svelte';
  import Node from './Node.svelte';
  import HubNode from './HubNode.svelte';
  import { offeringModel } from '../../lib/entityFigures.ts';
  import { countOf, formatCount } from '../../lib/format.ts';
  import { hashForHref } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ offering: string, providers: any, consumers: any, hidden: number, providersHref: string }} */
  let { offering, providers, consumers, hidden, providersHref } = $props();
  // A provider row carries its service; the figure draws the service.
  const m = $derived(offeringModel({ total: providers?.total, items: (providers?.items ?? []).map((/** @type {any} */ p) => p.service ?? {}) }, consumers?.phases));
  const failed = $derived(consumers?.failed ?? { items: [], total: 0 });
  const range = $derived(consumers?.range ?? {});
  const st = (/** @type {string} */ k) => m.stations.find((s) => s.key === k);
</script>

{#snippet station(/** @type {import('../../lib/entityFigures.ts').PhaseStation} */ s)}
  <div class="ph station ph-{s.key}">
    <Node tone={s.tone} hatch={s.hatch} glyph={s.glyph} big a="ph-{s.key}" m="ph-{s.key}" />
    <span class="ph-l">
      <b>{s.label}</b>
      <span data-nofilter><b>{formatCount(s.n)}</b> · {s.share}</span>
      <span class="t-meta">{s.sub}</span>
    </span>
    {#if s.key === 'provisioning'}<i class="anc" data-a="ph-provisioning-b"></i>{/if}
  </div>
{/snippet}

<Figure id="offering" testid="offering-figure" question="Who provides this, who uses it, and is their request ready?"
  alt={m.alt} wires={m.wires} rowMin={650}>
  {#snippet stage()}
    <div class="ofig">
      <ul class="ofig-prov" aria-label="Providers">
        {#each m.named as p, k (p.key)}
          <li><Node shape="square" glyph="provider" a="p{k}" /><a class="id" href={hashForHref(p.href)} title={p.label}><bdi class:fit={elidable(p.label ?? '')}>{p.label}</bdi></a></li>
        {/each}
        {#if m.more}
          <li><span class="cnt mk" data-a="pm" aria-hidden="true">+{formatCount(m.more)}</span><a href={providersHref}>{formatCount(m.more)} more {m.more === 1 ? 'provider' : 'providers'}</a></li>
        {/if}
      </ul>
      <div class="ofig-hub">
        <HubNode kind="offering" a="hub" />
        <span class="ofig-hl"><b class="id" title={offering}><bdi class:fit={elidable(offering)}>{offering}</bdi></b><span class="t-meta">{countOf(m.requests, 'request')}</span></span>
      </div>
      {@render station(st('requested'))}
      {@render station(st('provisioning'))}
      {@render station(st('ready'))}
      {@render station(st('deleting'))}
      {@render station(st('failed'))}
      {#if failed.count}
        <div class="ph-chips">
          {#each failed.items as c (c.key)}<a class="badge chip t-err" href={hashForHref(c.href)} title={c.label}><bdi class:fit={elidable(c.label ?? '')}>{c.label}</bdi></a>{/each}
          {#if failed.total > failed.count}<span class="t-meta" data-nofilter>{formatCount(failed.total - failed.count)} more failed {failed.total - failed.count === 1 ? 'consumer' : 'consumers'}</span>{/if}
        </div>
      {/if}
      {#if st('unknown').n}
        <div class="ph station ph-unknown">
          <Node tone="neutral" hatch glyph="unknown" a="ph-unknown" m="ph-unknown" />
          <span><span data-nofilter><b>{formatCount(st('unknown').n)}</b> not reported ({st('unknown').share})</span>: {st('unknown').sub}.</span>
        </div>
      {/if}
    </div>
  {/snippet}
  {#snippet aside()}
    <dl class="facts">
      <dt>Providers</dt><dd><a href={providersHref}>{formatCount(m.total)}</a></dd>
      <dt>Uses</dt><dd data-nofilter>{countOf(consumers?.total ?? 0, 'use')}: {formatCount(range.satisfied ?? 0)} in the accepted range, {formatCount(range.unsatisfied ?? 0)} outside it, {formatCount(range.unknown ?? 0)} unknown, {formatCount(range.none ?? 0)} not checked</dd>
      {#if hidden}<dt>You may not see</dt><dd data-nofilter>{countOf(hidden, 'consumer')}, not counted and never named</dd>{/if}
    </dl>
  {/snippet}
  {#snippet caption()}{countOf(m.total, 'provider')} {m.total === 1 ? 'offers' : 'offer'} it. {m.summary}{hidden ? ` ${countOf(hidden, 'consumer')} you may not see ${hidden === 1 ? 'is' : 'are'} not counted.` : ''} Phases are what each provider last reported, not a history.{/snippet}
</Figure>
