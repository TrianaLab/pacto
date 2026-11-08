<script>
  // The Delivery Autonomy figure (SPEC 7): "How much does delivery run on its own?". One
  // same-size card per level, each a flat drawing of what happens to one change (S9:
  // size never encodes the level), then its Stages as a count and a bar on one shared
  // scale. The aside says who approved the promotions. Ambient: no narration.
  import Figure from './Figure.svelte';
  import { autonomyModel } from '../../lib/autonomyFigure.ts';
  import { KIND_GLYPH } from '../../lib/figure.ts';
  import { formatCount, countOf } from '../../lib/format.ts';

  /** @type {{ byLevel: NonNullable<import('../../lib/api.ts').DeliveryAutonomy['byLevel']>, promotions: { count: number, of: number, untyped: number } }} */
  let { byLevel, promotions } = $props();
  const m = $derived(autonomyModel(byLevel, promotions));
</script>

{#snippet person(/** @type {number} */ x, /** @type {number} */ y)}
  <circle class="af-n" cx={x} cy={y} r="11" />
  <path class="af-g" transform="translate({x - 7.2} {y - 7.2}) scale(0.6)" d={KIND_GLYPH.owner} />
{/snippet}
{#snippet change(/** @type {number} */ x, /** @type {number} */ y)}<circle class="af-n af-acc" cx={x} cy={y} r="7" />{/snippet}
{#snippet env(/** @type {number} */ x, /** @type {number} */ y)}<rect class="af-n" x={x - 8} y={y - 8} width="16" height="16" rx="3" />{/snippet}

<Figure id="ladder" testid="autonomy-figure" question="How much does delivery run on its own?" alt={m.alt} compact={true}>
  {#snippet stage()}
    <ol class="lad" style:--n={m.cards.length}>
      {#each m.cards as c (c.level)}
        <li class="lv">
          <svg class="aflow mk" width="168" height="64" viewBox="0 0 168 64" aria-hidden="true" focusable="false">
            {#if c.level === 'observe'}
              <path class="af-l" d="M21 32 H146" /><path class="af-l af-dot" d="M84 32 V14" />
              {@render change(14, 32)}<circle class="af-n" cx="84" cy="10" r="5" />{@render env(154, 32)}
            {:else if c.level === 'advise'}
              <path class="af-l" d="M21 32 H73" /><path class="af-l" d="M95 32 H146" />
              {@render change(14, 32)}{@render person(84, 32)}{@render env(154, 32)}
            {:else if c.level === 'auto-nonbreaking'}
              <path class="af-l af-acc" d="M20 28 C56 2 112 2 147 27" /><path class="af-l" d="M20 36 L74 50" /><path class="af-l" d="M94 50 L147 37" />
              {@render change(14, 32)}{@render person(84, 52)}{@render env(154, 32)}
            {:else}
              <path class="af-l af-acc" d="M21 32 H73" /><path class="af-l af-acc" d="M95 32 H146" />
              {@render change(14, 32)}<path class="af-n" d="M84 21 L95 32 L84 43 L73 32 Z" />{@render env(154, 32)}
            {/if}
          </svg>
          <b class="lv-t">{c.label}</b>
          <span class="t-meta">{c.says}</span>
          <span><b data-nofilter>{formatCount(c.n)}</b> {c.n === 1 ? 'Stage' : 'Stages'}</span>
          <span class="cbar" aria-hidden="true"><i style:width="max(6px, {((c.n / m.max) * 100).toFixed(1)}%)" class:zero={!c.n}></i></span>
        </li>
      {/each}
    </ol>
  {/snippet}
  {#snippet aside()}
    <b>Who approved the promotions</b>
    <span class="sbar" aria-hidden="true">
      {#if m.approvals.person}<i class="t-accent" style:flex-grow={m.approvals.person}></i>{/if}
      {#if m.approvals.auto}<i class="t-neutral" style:flex-grow={m.approvals.auto}></i>{/if}
      {#if m.approvals.unrecorded}<i class="t-neutral hatch" style:flex-grow={m.approvals.unrecorded}></i>{/if}
    </span>
    <ul class="keyl">
      <li><i class="sw t-accent"></i><span>A person</span><b data-nofilter>{formatCount(m.approvals.person)}</b></li>
      <li><i class="sw t-neutral"></i><span>Automatic</span><b data-nofilter>{formatCount(m.approvals.auto)}</b></li>
      <li><i class="sw t-neutral hatch"></i><span>Not recorded</span><b data-nofilter>{formatCount(m.approvals.unrecorded)}</b></li>
    </ul>
    <p class="t-meta lad-note">Over the last 20 operations per Stage. Not recorded is never counted as automatic.{#if m.none}{' '}{countOf(m.none, 'Stage')} {m.none === 1 ? 'reports' : 'report'} no level.{/if}</p>
  {/snippet}
  {#snippet caption()}{m.caption}{' '}<span class="fig-ref">{m.figRef}</span>{/snippet}
</Figure>
