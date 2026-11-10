<script>
  // Lane (SPEC 3): one bucket of a disjoint partition. Its node, name, count and share
  // are always drawn; the share bar (96 to 160px, 8px floor) and the named chips drop by
  // width (S1); "N more" counts what the chips do not name. lib/figureLayout.ts folds the
  // chips into it in the figure's layout pass. The count and "N more" link to the list of
  // the lane's members (T3), whose total is the lane's count; `onpick` may handle a plain
  // click in place (the caller keeps the answer on screen).
  import Node from './Node.svelte';
  import { hashForHref } from '../../lib/router.ts';
  import { formatCount } from '../../lib/format.ts';
  import { sharePct } from '../../lib/figure.ts';
  import { elidable } from '../../lib/elide.ts';

  // A group lane (a rank figure's owner or domain) keeps a neutral node (S10) and
  // carries the severity in its bar (`barTone`); its name may be an identity (`ident`,
  // isolated and elided in its middle, C6) and its lane note says what its count is of,
  // in words with its own denominator (C14), or links what no count can (`note.href`).
  /** @type {{ k: number | string, tone: string, barTone?: string, hatch?: boolean, glyph?: any, label: string, ident?: boolean, n: number, total?: number, chips?: { key: string, label: string, href: string, ring: boolean }[], open?: boolean, decides?: boolean, href?: string, note?: { text: string, href?: string } | null, onpick?: ((e: MouseEvent) => void) | null }} */
  let { k, tone, barTone = '', hatch = false, glyph = '', label, ident = false, n, total = 0, chips = [], open = false, decides = false, href = '', note = null, onpick = null } = $props();
  const share = $derived(total ? n / total : 0);
  const pick = (/** @type {MouseEvent} */ e) => onpick?.(e);
</script>

<div class="lane" class:decides data-a="lane-{k}">
  <Node {tone} {hatch} {glyph} a="ln-{k}" m="ln-{k}" />
  {#if ident}
    <span class="lane-name id" title={label}><bdi class:fit={elidable(label)}>{label}</bdi></span>
  {:else}
    <span class="lane-name" class:words={label.length <= 20}>{label}</span>
  {/if}
  {#if href && n}
    <a class="lane-count" {href} onclick={pick}><b>{formatCount(n)}</b>{#if total}{` · ${sharePct(n, total)}`}{/if}</a>
  {:else}
    <span class="lane-count" data-nofilter><b>{formatCount(n)}</b>{#if total}{` · ${sharePct(n, total)}`}{/if}</span>
  {/if}
  <span class="mbar" aria-hidden="true">{#if n}<i class="t-{barTone || tone}" class:hatch style:width="max(8px, {(share * 100).toFixed(1)}%)"></i>{/if}</span>
  <span class="opencap-cell" aria-hidden="true">{#if open}<svg class="opencap" width="12" height="16" viewBox="0 0 12 16"><path d="M4 1 L1.5 15 M10.5 1 L8 15" /></svg>{/if}</span>
  <span class="lane-list">
    <span class="lane-chips">
      {#each chips as c (c.key)}
        <a class="badge chip t-{tone}" class:unk={hatch} href={hashForHref(c.href)} title={c.label} data-ring={c.ring || undefined}><bdi class:fit={elidable(c.label)}>{c.label}</bdi></a>
      {/each}
    </span>
    {#if n && href}<a class="lane-more" {href} data-n={n} onclick={pick}>{chips.length ? (n > chips.length ? `${formatCount(n - chips.length)} more` : '') : `List all ${formatCount(n)}`}</a>{/if}
  </span>
  <!-- A note wraps on its own line under the lane, never squeezed beside it (C8). -->
  {#if note}{#if note.href}<a class="lane-note" href={note.href}>{note.text}</a>{:else}<span class="lane-note t-meta" data-nofilter>{note.text}</span>{/if}{/if}
</div>
