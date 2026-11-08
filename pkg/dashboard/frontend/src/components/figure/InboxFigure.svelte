<script>
  // The Delivery Inbox figure (SPEC 7): "Where are changes stuck on their way to
  // production?". Equal neutral stations (S10), one per destination environment in the
  // order the Stages declare; under each, a dashed queue of what waits to enter it, one
  // bar per kind on one shared scale (S9), each linking to the lists filtered to it (T3).
  // The queues are the one carrier of the counts; the caption gives the totals once.
  import Figure from './Figure.svelte';
  import Station from './Station.svelte';
  import { inboxModel, KINDS } from '../../lib/inboxFigure.ts';
  import { GLYPH_PATH } from '../../lib/figure.ts';
  import { formatCount } from '../../lib/format.ts';

  /** @type {{ byEnvironment: NonNullable<import('../../lib/api.ts').DeliveryInbox['byEnvironment']>, href: (env: string, kind: string) => string }} */
  let { byEnvironment, href } = $props();
  const m = $derived(inboxModel(byEnvironment));
  const w = $derived(m.stations.length > 4 ? 80 : 96);
</script>

<Figure id="inbox" testid="inbox-figure" question="Where are changes stuck on their way to production?"
  alt={m.alt} wires={m.wires} steps={m.steps} rowMin={m.stations.length * 150} autoplay={m.steps.length > 0} autoKey={m.key}>
  {#snippet stage()}
    <ol class="pipe" style:--n={m.stations.length}>
      {#each m.stations as s, i (s.key)}
        <li class="pst station">
          <Station a="p{i}" m="p{i}" {w} />
          <div class="pst-l">
            <b class="pst-name"><bdi>{s.label}</bdi></b>
            <div class="queue">
              <span class="queue-h">Waiting to enter <bdi>{s.label}</bdi></span>
              {#each KINDS.filter((k) => s[k.key]) as k (k.key)}
                {@const body = { n: s[k.key], k }}
                {#if s.env}
                  <a class="qrow" href={href(s.env, k.key)}>{@render row(body, i)}</a>
                {:else}
                  <span class="qrow" data-nofilter>{@render row(body, i)}</span>
                {/if}
              {/each}
            </div>
          </div>
        </li>
      {/each}
    </ol>
  {/snippet}
  {#snippet caption()}{m.caption}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>

{#snippet row(/** @type {{ n: number, k: (typeof KINDS)[number] }} */ b, /** @type {number} */ i)}
  <svg class="qglyph t-{b.k.tone}" width="16" height="16" viewBox="-8 -8 16 16" aria-hidden="true" focusable="false"><circle r="7" /><path d={GLYPH_PATH[b.k.glyph]} /></svg>
  {#if m.stations[i].key !== '+'}<span class="qbar" aria-hidden="true"><i class="t-{b.k.tone}" data-m="qb{i}{b.k.key}" style:width="max(14px, {((b.n / m.max) * 100).toFixed(1)}%)"></i></span>{/if}
  <span class="qn"><b>{formatCount(b.n)}</b> {b.k.word}</span>
{/snippet}
