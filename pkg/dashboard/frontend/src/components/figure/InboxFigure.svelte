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
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ byEnvironment: NonNullable<import('../../lib/api.ts').DeliveryInbox['byEnvironment']>, href: (env: string, kind: string, except?: string[]) => string, partial?: boolean }} */
  let { byEnvironment, href, partial = false } = $props();
  const m = $derived(inboxModel(byEnvironment));
  const w = $derived(m.stations.length > 4 ? 80 : 96);
  // S5a (r5): a partial listing makes every queue a lower bound: each bar ends in an
  // open cap, and the caption says so beside the same glyph.
  // The folded station's name and counts link to the lists of every environment but
  // the ones drawn (S2, T3), so each opens exactly what it counts.
</script>

<Figure id="inbox" testid="inbox-figure" question="Where are changes stuck on their way to production?"
  alt={m.alt} wires={m.wires} steps={m.steps} rowMin={m.stations.length * 150} autoplay={m.steps.length > 0} autoKey={m.key} compact={true}>
  {#snippet stage()}
    <ol class="pipe" style:--n={m.stations.length}>
      {#each m.stations as s, i (s.key)}
        <li class="pst station">
          <Station a="p{i}" m="p{i}" {w} />
          <div class="pst-l">
            {#if s.except}<a class="pst-name" href={href('', '', s.except)}>{s.label}</a>{:else if s.env}<b class="pst-name id" title={s.label}><bdi class:fit={elidable(s.label)}>{s.label}</bdi></b>{:else}<b class="pst-name"><bdi>{s.label}</bdi></b>{/if}
            <div class="queue">
              <span class="queue-h">Waiting to enter <bdi>{s.label}</bdi></span>
              {#each KINDS.filter((k) => s[k.key]) as k (k.key)}
                {@const body = { n: s[k.key], k }}
                {#if s.env || s.key === '+'}
                  <a class="qrow" href={href(s.env, k.key, s.except)}>{@render row(body, i)}</a>
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
  {#snippet caption()}{m.caption}{#if partial}{' '}{@render cap()}Namespaces you cannot list are not counted, so each queue may hold more.{/if}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>

{#snippet row(/** @type {{ n: number, k: (typeof KINDS)[number] }} */ b, /** @type {number} */ i)}
  <svg class="qglyph t-{b.k.tone}" width="16" height="16" viewBox="-8 -8 16 16" aria-hidden="true" focusable="false"><circle r="7" /><path d={GLYPH_PATH[b.k.glyph]} /></svg>
  {#if m.stations[i].key !== '+'}<span class="qbar" aria-hidden="true"><i class="t-{b.k.tone}" data-m="qb{i}{b.k.key}" style:width="max(14px, {((b.n / m.max) * 100).toFixed(1)}%)"></i></span>{#if partial}{@render cap()}{/if}{/if}
  <span class="qn"><b>{formatCount(b.n)}</b> {b.k.word}</span>
{/snippet}

{#snippet cap()}<svg class="opencap qcap" width="12" height="16" viewBox="0 0 12 16" aria-hidden="true" focusable="false"><path d="M4 1 L1.5 15 M10.5 1 L8 15" /></svg>{/snippet}
