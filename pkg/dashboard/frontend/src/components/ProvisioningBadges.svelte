<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import { phaseBadgeLabel, phaseGlyph, phaseTone, provisioningTitle } from '../lib/provisioning.ts';
  import { GLYPH_PATH } from '../lib/figure.ts';

  // What providers report about the resource one offering use asked for, one badge
  // per environment and, when the use has several, per consumer entry. The row shows the phase; the title carries who reported it,
  // through which source, signed or not, when, for which consumer revision and the
  // external reference, so a long reference never widens the row. The phase also has a
  // glyph drawn as geometry (S6, C15), so it never depends on colour alone.
  // With `onselect`, each badge is a toggle button that opens that request's rail
  // (the offering use detail); `selected` is the index shown, -1 for none.
  /** @type {{ statuses?: any[], onselect?: ((i: number) => void) | null, selected?: number }} */
  let { statuses = [], onselect = null, selected = -1 } = $props();
</script>

{#each statuses as st, i (`${st.environment ?? ''}:${st.entry ?? ''}:${i}`)}
  {#snippet badge()}
    <svg class="pb-g pb-{phaseTone(st.phase)}" width="14" height="14" viewBox="-7 -7 14 14" aria-hidden="true" focusable="false"><path d={GLYPH_PATH[phaseGlyph(st.phase)]} /></svg>
    <IdentityBadge label={phaseBadgeLabel(st)} tone={phaseTone(st.phase)} title={provisioningTitle(st)} describe />
  {/snippet}
  {#if onselect}
    <button type="button" class="pb pb-btn" data-testid="provisioning-badge" aria-pressed={selected === i} onclick={() => onselect(selected === i ? -1 : i)}>{@render badge()}</button>
  {:else}
    <span class="pb" data-testid="provisioning-badge">{@render badge()}</span>
  {/if}
{/each}

<style>
  .pb { display: inline-flex; align-items: center; gap: 4px; min-width: 0; max-width: 100%; }
  .pb-btn { padding: 2px; border: 1px solid transparent; border-radius: var(--radius-sm); background: none; color: inherit; font: inherit; cursor: pointer; }
  .pb-btn:hover { border-color: var(--c-text-3); }
  .pb-btn[aria-pressed='true'] { border-color: var(--c-accent); }
  .pb-g { flex: none; fill: none; stroke: var(--c-neutral); stroke-width: 1.8; stroke-linecap: round; stroke-linejoin: round; }
  .pb-ok { stroke: var(--c-ok); }
  .pb-err { stroke: var(--c-err); }
  .pb-info { stroke: var(--c-info); }
</style>
