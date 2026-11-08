<script>
  // Lane (SPEC 3): one bucket of a disjoint partition. Its node, name, count and share
  // are always drawn; the share bar (96 to 160px, 8px floor) and the named chips drop by
  // width (S1); "N more" counts what the chips do not name. lib/figureLayout.ts folds the
  // chips into it in the figure's layout pass. The count is not a filter (data-nofilter):
  // the consumers table below cannot filter by outcome yet.
  import Node from './Node.svelte';
  import { hashForHref } from '../../lib/router.ts';
  import { formatCount } from '../../lib/format.ts';
  import { sharePct } from '../../lib/figure.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ k: number, tone: string, hatch?: boolean, glyph?: any, label: string, n: number, total?: number, chips?: { key: string, label: string, href: string, ring: boolean }[], ghost?: boolean, open?: boolean, decides?: boolean, onmore?: (() => void) | null }} */
  let { k, tone, hatch = false, glyph = '', label, n, total = 0, chips = [], ghost = false, open = false, decides = false, onmore = null } = $props();
  const share = $derived(total ? n / total : 0);
</script>

<div class="lane" class:ghost class:decides data-a="lane-{k}">
  <Node {tone} {hatch} {glyph} a="ln-{k}" m="ln-{k}" />
  <span class="lane-name" class:words={label.length <= 20}>{label}</span>
  <span class="lane-count" data-nofilter><b>{formatCount(n)}</b>{#if total}{` · ${sharePct(n, total)}`}{/if}</span>
  {#if !ghost}
    <span class="mbar" aria-hidden="true">{#if n}<i class="t-{tone}" class:hatch style:width="max(8px, {(share * 100).toFixed(1)}%)"></i>{/if}</span>
    <span class="opencap-cell" aria-hidden="true">{#if open}<svg class="opencap" width="12" height="16" viewBox="0 0 12 16"><path d="M4 1 L1.5 15 M10.5 1 L8 15" /></svg>{/if}</span>
    <span class="lane-list">
      <span class="lane-chips">
        {#each chips as c (c.key)}
          <a class="badge chip t-{tone}" class:unk={hatch} href={hashForHref(c.href)} title={c.label} data-ring={c.ring || undefined}><bdi class:fit={elidable(c.label)}>{c.label}</bdi></a>
        {/each}
      </span>
      {#if n && onmore}<button type="button" class="lane-more" data-n={n} onclick={onmore}>{chips.length ? (n > chips.length ? `${formatCount(n - chips.length)} more` : '') : `List all ${formatCount(n)}`}</button>{/if}
    </span>
  {/if}
</div>
