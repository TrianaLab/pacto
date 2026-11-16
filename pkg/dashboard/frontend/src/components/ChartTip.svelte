<script lang="ts">
  /**
   * The page's one chart tooltip (#597): renders lib/chartTip's store. Words go in as
   * text nodes only (labels are fleet data), the value leads and the label follows,
   * series wear a line key and unknown or unverified wear their texture. It stays inside
   * the viewport: beside the pointer, flipped below when there is no room above.
   */
  import { tipState, hideTip, TIP_ID } from '../lib/chartTip.ts';

  let el: HTMLDivElement | undefined = $state();
  let left = $state(0);
  let top = $state(0);

  $effect(() => {
    const s = $tipState;
    if (!s || !el) return;
    const r = el.getBoundingClientRect();
    left = Math.min(Math.max(8, s.x + 14), innerWidth - r.width - 8);
    top = s.y - r.height - 12 < 8 ? s.y + 16 : s.y - r.height - 12;
  });

  function outside(e: PointerEvent) {
    const owner = $tipState?.owner;
    if (owner && !owner.contains(e.target as Node)) hideTip();
  }
</script>

<svelte:window
  onkeydown={(e) => { if (e.key === 'Escape' && $tipState) hideTip(); }}
  onpointerdown={outside}
/>

<div id={TIP_ID} class="chart-tip" class:on={!!$tipState} role="tooltip" bind:this={el} style:left="{left}px" style:top="{top}px">
  {#if $tipState}
    {@const m = $tipState.model}
    <div class="tt">{m.title}</div>
    {#each m.rows as r, i (i)}
      <div class="row">
        {#if r.texture}<span class="lk tex {r.texture}" aria-hidden="true"></span>
        {:else if r.color}<span class="lk" style:background={r.color} aria-hidden="true"></span>{/if}
        <b>{r.value}</b><span>{r.label}</span>
      </div>
    {/each}
    {#if m.foot}<div class="foot">{m.foot}</div>{/if}
  {/if}
</div>

<style>
  .chart-tip {
    position: fixed; z-index: 1000; pointer-events: none;
    min-width: 18ch; max-width: min(36ch, calc(100vw - 16px));
    padding: var(--sp-2) var(--sp-3);
    background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); box-shadow: var(--shadow-md);
    font-size: var(--text-xs); color: var(--c-text-2); overflow-wrap: anywhere;
    opacity: 0; visibility: hidden; transition: opacity var(--dur-1);
  }
  .chart-tip.on { opacity: 1; visibility: visible; }
  .tt { font-weight: 600; color: var(--c-text); margin-bottom: var(--sp-1); }
  .row { display: flex; align-items: center; gap: var(--sp-2); line-height: 1.7; }
  .row b { color: var(--c-text); font-size: var(--text-sm); font-variant-numeric: tabular-nums; min-width: 2.5ch; }
  .lk { inline-size: 12px; block-size: 2px; border-radius: 1px; flex: none; }
  .lk.tex { block-size: 8px; }
  .foot { margin-top: var(--sp-1); color: var(--c-text-3); }
</style>
