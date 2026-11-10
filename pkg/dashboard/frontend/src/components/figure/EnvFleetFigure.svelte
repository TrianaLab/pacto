<script>
  // The fleet Environments figure (SPEC 7, SPEC 21): "Where do the services stand on
  // their way to production?". Equal neutral stations (S10) in the declared promotion
  // order; under each, its Stages by state on one share bar and a count list, each count
  // linking to the board filtered to it (T3). The folded station's name and counts open
  // the board of every environment but the ones drawn (S2), so each lists what it counts.
  // S5a: a partial listing makes every count a lower bound, so each share bar ends in an
  // open cap and the caption says so beside the same glyph. Ambient: no narration.
  import Figure from './Figure.svelte';
  import Station from './Station.svelte';
  import { fleetEnvModel } from '../../lib/envFigure.ts';
  import { GLYPH_PATH } from '../../lib/figure.ts';
  import { formatCount, countOf } from '../../lib/format.ts';
  import { elidable } from '../../lib/elide.ts';
  import { andList } from '../../lib/delivery.ts';

  // No aside: seven stations need the figure's whole width, so their counts keep one line.
  /** @type {{ byEnvironment: NonNullable<import('../../lib/api.ts').DeliveryEnvironments['counts']['byEnvironment']>, href: (state: string, env: string, except?: string[]) => string, partial?: boolean, asOf?: string }} */
  let { byEnvironment, href, partial = false, asOf = '' } = $props();
  const m = $derived(fleetEnvModel(byEnvironment));
  const w = $derived(m.stations.length > 4 ? 80 : 96);
</script>

<Figure id="envs" testid="env-fleet-figure" question="Where do the services stand on their way to production?"
  alt={m.alt} wires={m.wires} rowMin={m.stations.length * 150} compact={true}>
  {#snippet stage()}
    <ol class="pipe" style:--n={m.stations.length}>
      {#each m.stations as s, i (s.key)}
        <li class="pst station">
          <Station a="e{i}" m="e{i}" {w} />
          <div class="pst-l">
            {#if s.env}<b class="pst-name id" title={s.label}><bdi class:fit={elidable(s.label)}>{s.label}</bdi></b>{:else}<a class="pst-name" href={href('', '', s.except)}>{s.label}</a>{/if}
            <span class="queue-h"><a href={href('', s.env, s.except)}>{countOf(s.stages, 'Stage')}</a>{#if s.tier}, named like {s.tier}{/if}</span>
            {#if s.fedBy.length}<span class="queue-h pst-fed">fed by <bdi class="id">{andList(s.fedBy, 3)}</bdi></span>{/if}
            <span class="sbar-row"><span class="sbar" aria-hidden="true">{#each s.counts as c (c.s.key)}<i class="t-{c.s.tone}" class:hatch={c.s.hatch} style:flex-grow={c.n}></i>{/each}</span>{#if partial}{@render cap()}{/if}</span>
            <ul class="envc">
              {#each s.counts as c (c.s.key)}
                <li>
                  <svg class="qglyph t-{c.s.tone}" class:unk={c.s.hatch} width="16" height="16" viewBox="-8 -8 16 16" aria-hidden="true" focusable="false"><circle r="7" /><path d={GLYPH_PATH[c.s.glyph]} /></svg>
                  <a href={href(c.s.key, s.env, s.except)}><b>{formatCount(c.n)}</b></a>
                  <span>{c.s.word}</span>
                </li>
              {/each}
            </ul>
          </div>
        </li>
      {/each}
    </ol>
  {/snippet}
  {#snippet caption()}{m.caption}{#if partial}{' '}{@render cap()}Namespaces you cannot list are not counted, so each environment may hold more Stages.{/if}{' '}The order is the upstream each Stage declares; a tier is only what a name says.{#if asOf}{' '}As of {asOf}.{/if}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>

{#snippet cap()}<svg class="opencap qcap" width="12" height="16" viewBox="0 0 12 16" aria-hidden="true" focusable="false"><path d="M4 1 L1.5 15 M10.5 1 L8 15" /></svg>{/snippet}
