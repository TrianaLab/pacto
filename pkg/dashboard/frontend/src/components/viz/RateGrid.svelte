<script lang="ts">
  /**
   * The delivery-health heatmap (#597): environments x rates, an HTML table under group
   * rows that double as the group's pooled summary. Every cell keeps its table semantics
   * and carries its count, denominator and percent in words (visually hidden), so the
   * colour is never the only carrier; the card's table view prints every number too.
   *
   * A rate with nothing to count (`of === 0`) is an empty hatch underlay with "unknown"
   * on an opaque plate, and keeps its stray count ("2 of 0"); a real 0 is the neutral
   * cell; otherwise one of 4 ordinal bins. The `.hm-scroll` wrapper is the page's only
   * horizontal scroller. The grid is one tab stop with a roving tabindex: arrows move,
   * Home and End jump within a row. Paging is local (`more`), never the URL pager.
   */
  import { chartTip, type TipModel } from '../../lib/chartTip.ts';
  import { fitIdents } from '../../lib/elide.ts';
  import { HEAT } from '../../lib/chart.ts';

  type CellModel = { count: number; of: number; bin?: 1 | 2 | 3 | 4; foot: string };
  type Column = { key: string; label: string };
  type Action = { label: string; disabled?: boolean; onclick: () => void };
  type Group = {
    key: string; label: string; title: string; count: number;
    summary: Record<string, CellModel>;
    rows: { name: string; cells: Record<string, CellModel> }[];
    open: boolean;
    more?: { label: string; actions: Action[] };
  };

  let {
    columns = [] as Column[],
    groups = [] as Group[],
    ontoggle = (_key: string) => {},
    emptyText = 'Nothing to show.',
    nameLabel = 'Environment',
    noun = 'promotions',
  } = $props();

  let tableEl: HTMLTableElement | undefined = $state();

  const pct = (c: CellModel) => Math.round((c.count / c.of) * 100);
  // `bin` is required when count > 0 (lib/metrics.ts owns the bin edges).
  const bin = (c: CellModel) => c.bin ?? 1;
  const words = (c: CellModel) => (c.of === 0 ? `unknown, ${c.count} of 0 ${noun}` : `${c.count} of ${c.of} ${noun}, ${pct(c)}%`);

  function tip(c: CellModel, title: string): TipModel {
    if (c.of === 0) return { title, rows: [{ value: 'unknown', label: 'nothing to count', texture: 'hatch' }], foot: [`${c.count} of 0 ${noun}`, c.foot].filter(Boolean).join('. ') };
    return {
      title,
      rows: [{ value: String(c.count), label: `of ${c.of} ${noun}`, ...(c.count ? { color: `var(${HEAT[bin(c) - 1]})` } : {}) }],
      foot: c.foot || `${pct(c)}%, n = ${c.of}`,
    };
  }

  // Roving tabindex: one cell holds tabindex 0; the rest are -1 (a click still focuses).
  const cellsIn = (tr: Element) => [...tr.querySelectorAll<HTMLElement>('.cell')];
  function roveTo(next: HTMLElement | undefined) {
    if (!next || !tableEl) return;
    for (const c of tableEl.querySelectorAll<HTMLElement>('.cell')) c.tabIndex = c === next ? 0 : -1;
    next.focus();
  }
  function onkeydown(e: KeyboardEvent) {
    const cell = (e.target as Element).closest<HTMLElement>('.cell');
    if (!cell || !tableEl) return;
    const rowsWithCells = [...tableEl.querySelectorAll('tr')].filter((tr) => tr.querySelector('.cell'));
    const tr = cell.closest('tr')!;
    const r = rowsWithCells.indexOf(tr), row = cellsIn(tr), c = row.indexOf(cell);
    const at = (ri: number) => cellsIn(rowsWithCells[ri] ?? tr);
    const moves: Record<string, () => HTMLElement | undefined> = {
      ArrowRight: () => row[Math.min(c + 1, row.length - 1)],
      ArrowLeft: () => row[Math.max(c - 1, 0)],
      ArrowDown: () => at(Math.min(r + 1, rowsWithCells.length - 1))[c],
      ArrowUp: () => at(Math.max(r - 1, 0))[c],
      Home: () => row[0],
      End: () => row[row.length - 1],
    };
    if (!moves[e.key]) return;
    e.preventDefault();
    roveTo(moves[e.key]());
  }
  const onfocusin = (e: FocusEvent) => {
    const cell = (e.target as Element).closest<HTMLElement>('.cell');
    if (cell && cell.tabIndex !== 0 && tableEl) for (const c of tableEl.querySelectorAll<HTMLElement>('.cell')) c.tabIndex = c === cell ? 0 : -1;
  };

  // After each render: new cells start at -1, and exactly one keeps tabindex 0.
  $effect(() => {
    void groups; void columns;
    if (!tableEl) return;
    const all = [...tableEl.querySelectorAll<HTMLElement>('.cell')];
    for (const c of all) if (!c.hasAttribute('tabindex')) c.tabIndex = -1;
    if (all.length && !all.some((c) => c.tabIndex === 0)) all[0].tabIndex = 0;
    const el = tableEl;
    fitIdents(el);
    // Again once web fonts land: a name measured in the fallback font can still overflow.
    document.fonts?.ready.then(() => { if (el.isConnected) fitIdents(el); });
  });
</script>

{#snippet cell(c: CellModel, title: string)}
  <span class="cell {c.of === 0 ? 'unk' : c.count ? `b${bin(c)}` : 'zero'}" use:chartTip={{ ...tip(c, title), role: false, focusable: false }}>
    {#if c.of === 0}<span class="hatch" aria-hidden="true"></span><span class="plate" aria-hidden="true">unknown</span>{/if}
    <span class="visually-hidden">{title}: {words(c)}</span>
  </span>
{/snippet}

<div class="rg">
  <!-- A scrolling region must be reachable by keyboard to scroll (WCAG 2.1.1). -->
  <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
  <div class="hm-scroll" tabindex="0" role="region" aria-label="Delivery health heatmap, scrolls in both directions">
    <!-- svelte-ignore a11y_no_noninteractive_element_interactions (the cells hold the roving tabindex; keys are delegated here) -->
    <table class="hm" bind:this={tableEl} style:--cols={columns.length} {onkeydown} {onfocusin}>
      <colgroup><col class="name" />{#each columns as c (c.key)}<col />{/each}</colgroup>
      <thead>
        <tr><th scope="col">{nameLabel}</th>{#each columns as c (c.key)}<th scope="col">{c.label}</th>{/each}</tr>
      </thead>
      {#each groups as g (g.key)}
        <tbody>
          <tr class="rung">
            <th scope="rowgroup">
              <button type="button" aria-expanded={g.open} title={g.title} onclick={() => ontoggle(g.key)}>
                <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M1 3l4 4 4-4" fill="none" stroke="currentColor" stroke-width="1.6" /></svg>
                <bdi>{g.label}</bdi><span class="n">{g.count}</span>
              </button>
            </th>
            {#each columns as c (c.key)}<td>{@render cell(g.summary[c.key], `${c.label} in ${g.label}`)}</td>{/each}
          </tr>
          {#if g.open}
            {#each g.rows as r (r.name)}
              <tr>
                <th scope="row"><span class="nm" title={r.name}><bdi class="fit">{r.name}</bdi></span></th>
                {#each columns as c (c.key)}<td>{@render cell(r.cells[c.key], `${c.label} in ${r.name}`)}</td>{/each}
              </tr>
            {/each}
            {#if g.more}
              <tr class="more">
                <td colspan={columns.length + 1}>
                  <div class="pager">
                    {#if g.more.label}<span>{g.more.label}</span>{/if}
                    {#each g.more.actions as a (a.label)}<button type="button" disabled={a.disabled} onclick={a.onclick}>{a.label}</button>{/each}
                  </div>
                </td>
              </tr>
            {/if}
          {/if}
        </tbody>
      {/each}
      {#if !groups.length}
        <tbody><tr><td class="empty" colspan={columns.length + 1}>{emptyText}</td></tr></tbody>
      {/if}
    </table>
  </div>
</div>

<style>
  .rg { container-type: inline-size; min-inline-size: 0; }
  .hm-scroll {
    inline-size: fit-content; max-inline-size: 100%; max-block-size: min(520px, 70dvh); overflow: auto;
    /* Only the horizontal scroll is held: a vertical swipe at its end moves the page. */
    border: 1px solid var(--c-border); border-radius: var(--radius-sm); overscroll-behavior-x: contain;
  }
  .hm { --name-w: 34ch; inline-size: calc(var(--name-w) + var(--cols) * 13ch); border-collapse: separate; border-spacing: 0; table-layout: fixed; }
  .hm col.name { inline-size: var(--name-w); }
  .hm col:not(.name) { inline-size: 13ch; }
  @container (max-width: 60ch) { .hm { --name-w: 17ch; } }
  .hm th, .hm td { padding: 2px 3px; border: 0; text-align: center; }
  .hm thead th {
    position: sticky; top: 0; z-index: 2; background: var(--c-surface); padding: 6px 4px;
    font-size: var(--text-xs); font-weight: 600; color: var(--c-text-2); line-height: 1.25; vertical-align: bottom;
    border-bottom: 1px solid var(--c-border);
  }
  .hm thead th:first-child { left: 0; z-index: 3; text-align: left; }
  /* Names are values, not labels: the global th small caps would turn `dev` into DEV. */
  .hm tbody th {
    position: sticky; left: 0; z-index: 1; background: var(--c-surface); text-align: left; padding-left: var(--sp-2);
    font: 400 var(--text-sm) / 1.3 var(--font-sans); font-variant-caps: normal; letter-spacing: normal; color: var(--c-text-2);
  }
  .hm .nm { display: block; min-inline-size: 0; }
  .hm .nm .fit { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .hm tr.rung th { padding: var(--sp-2) var(--sp-2) 2px; max-inline-size: var(--name-w); }
  .hm tr.rung button {
    max-inline-size: 100%; overflow: hidden; white-space: nowrap; text-overflow: ellipsis;
    display: inline-flex; align-items: center; gap: var(--sp-2); min-block-size: 30px; border: 0; background: none; padding: 0; cursor: pointer;
    font: 600 var(--text-base) / 1.4 var(--font-display); font-variant-caps: all-small-caps; letter-spacing: .05em; color: var(--c-text-2);
  }
  .hm tr.rung button .n { font: 400 var(--text-xs) var(--font-sans); color: var(--c-text-3); letter-spacing: 0; font-variant-caps: normal; }
  .hm tr.rung svg { transition: transform 160ms; flex: none; }
  .hm tr.rung button[aria-expanded="false"] svg { transform: rotate(-90deg); }
  .cell { position: relative; display: block; block-size: 22px; border-radius: 3px; outline: none; }
  .hm tr.rung .cell { block-size: 14px; margin-top: 6px; }
  .cell.zero { background: var(--c-surface-raised); box-shadow: inset 0 0 0 1px var(--c-border); }
  .cell.b1 { background: var(--chart-hm-1); }
  .cell.b2 { background: var(--chart-hm-2); }
  .cell.b3 { background: var(--chart-hm-3); }
  .cell.b4 { background: var(--chart-hm-4); }
  /* Unknown: an empty hatch underlay; the word sits on its own opaque plate. */
  .cell.unk .hatch { position: absolute; inset: 0; border-radius: 3px; opacity: .55; }
  .cell.unk .plate {
    position: absolute; top: 50%; left: 50%; transform: translate(-50%, -50%); max-inline-size: calc(100% - 4px);
    overflow: hidden; white-space: nowrap; padding: 0 3px; border-radius: 2px; line-height: 1.3;
    font-size: var(--text-xs); color: var(--c-text); background: var(--c-surface);
  }
  .hm tr.rung .cell.unk .plate { display: none; }
  .cell:hover, .cell:focus-visible { outline: 2px solid var(--c-text); outline-offset: 1px; }
  .hm tr.more td { text-align: left; padding: 2px var(--sp-2) var(--sp-2); }
  .pager { position: sticky; left: var(--sp-2); display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); font-size: var(--text-xs); color: var(--c-text-3); }
  .pager button {
    min-block-size: 32px; padding: 0 var(--sp-2); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: none; color: var(--c-text-2); font-size: var(--text-xs); cursor: pointer;
  }
  .pager button:disabled { opacity: .45; cursor: default; }
  @media (pointer: coarse) { .pager button, .hm tr.rung button { min-block-size: 44px; } }
  .empty { padding: var(--sp-3); color: var(--c-text-3); font-size: var(--text-sm); }
</style>
