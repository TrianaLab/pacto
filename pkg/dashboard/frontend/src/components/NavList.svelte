<script>
  // The grouped primary navigation (SPEC 20, B2), drawn once for the sidebar (prefix 's')
  // and once for the phone drawer (prefix 'd'). Each link's accessible name is its label;
  // the attention count and the source health are descriptions, so the name a reader
  // hears, the rail tooltip and the expanded row all say the same words.
  import { navCurrent, navCount, railCount, sourceHealth, NAV_GLYPH } from '../lib/nav.ts';
  import { formatCount, countOf } from '../lib/format.ts';
  import { currentEntity } from '../lib/currentEntity.svelte.ts';

  let { groups = [], view = 'list', entityKind = '', attentionTotal = null, sourceCounts = null, prefix = 's', onNavigate = undefined } = $props();

  const pageKind = $derived(entityKind === 'service' && currentEntity.template ? 'golden-path' : entityKind);
  const health = $derived(sourceHealth(sourceCounts));
  const attention = $derived(typeof attentionTotal === 'number' && attentionTotal > 0 ? attentionTotal : 0);

  function describe(key) {
    if (key === 'attention' && attention) return `${countOf(attention, 'item')} to look at`;
    // Every state the ring shows is said: a stale or partial source reports, so the
    // reporting count alone read as all healthy.
    if (key === 'sources' && health) {
      return [`${formatCount(health.reporting)} of ${countOf(health.total, 'source')} reporting`,
        health.stale && `${formatCount(health.stale)} stale`, health.incomplete && `${formatCount(health.incomplete)} partial`].filter(Boolean).join(', ');
    }
    return '';
  }
</script>

<nav class="navlist" aria-label="Primary">
  {#each groups as g, i}
    <div class="nav-group">
      {#if g.label}<p class="nav-group-h" id="{prefix}-navg{i}">{g.label}</p>{/if}
      <ul aria-labelledby={g.label ? `${prefix}-navg${i}` : undefined}>
        {#each g.items as item (item.key)}
          {@const desc = describe(item.key)}
          <li>
            <a class="nav-item" href={item.href} aria-current={navCurrent(item, view, pageKind)}
              aria-describedby={desc ? `${prefix}-navd-${item.key}` : undefined} onclick={onNavigate} data-nav={item.key}>
              <span class="nav-ic">
                <svg class="kind-glyph kind-glyph--sm" viewBox="0 0 24 24" aria-hidden="true" focusable="false"><path d={NAV_GLYPH[item.glyph]} /></svg>
                {#if item.key === 'sources' && health}<i class="nav-dot" class:nav-part={health.partial} data-testid="nav-source-health"></i>{/if}
              </span>
              <span class="nav-label">{item.label}{#if desc}<span class="nav-tipx" aria-hidden="true">: {desc}</span>{/if}</span>
              {#if item.key === 'attention' && attention}
                <span class="nav-cnt" aria-hidden="true" data-testid="nav-attention-count">{navCount(attention)}</span>
                <span class="nav-bdg" aria-hidden="true">{railCount(attention)}</span>
              {/if}
              {#if item.key === 'sources' && health}<span class="nav-meta" aria-hidden="true">{formatCount(health.reporting)} of {formatCount(health.total)}</span>{/if}
            </a>
            {#if desc}<span id="{prefix}-navd-{item.key}" hidden>{desc}</span>{/if}
          </li>
        {/each}
      </ul>
    </div>
  {/each}
</nav>

<style>
  .navlist ul { list-style: none; margin: 0; padding: 0; }
  .nav-group + .nav-group { margin-top: var(--sp-3); }
  .nav-group-h {
    margin: 0; padding: var(--sp-1) var(--sp-2) 2px;
    font: var(--role-label-font); font-variant-caps: all-small-caps; letter-spacing: .05em; color: var(--c-text-3);
  }
  .nav-item {
    position: relative; display: flex; align-items: center; gap: var(--sp-2);
    min-height: 36px; padding: 6px var(--sp-2); border-radius: var(--radius-sm);
    border-left: 2px solid transparent; color: var(--c-text-2); font-size: var(--text-sm); text-decoration: none;
  }
  .nav-item:hover { background: var(--c-surface-hover); color: var(--c-text); text-decoration: none; }
  /* The weight never changes on selection, so a row never reflows: the accent
     background, the rule and the accent glyph mark it. */
  .nav-item[aria-current] { background: var(--c-accent-bg); color: var(--c-text); border-left-color: var(--c-accent); }
  .nav-ic { position: relative; display: inline-flex; flex: none; color: var(--c-text-3); }
  .nav-item[aria-current] .nav-ic { color: var(--c-accent); }
  .nav-label { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .nav-cnt {
    flex: none; font-size: var(--text-xs); font-weight: 700; line-height: 1.5; color: var(--c-err);
    background: var(--c-err-bg); border: 1px solid var(--c-err-border); border-radius: 999px; padding: 0 7px;
  }
  .nav-meta { flex: none; font-size: var(--text-xs); color: var(--c-text-3); white-space: nowrap; }
  /* Partial is a ring and current a disc: the shape carries the state, not only the colour. */
  .nav-dot { position: absolute; right: -3px; bottom: -2px; width: 9px; height: 9px; border-radius: 50%; background: var(--c-ok); box-shadow: 0 0 0 2px var(--c-surface); }
  .nav-dot.nav-part { background: var(--c-surface); border: 2px solid var(--c-warn); }
  .nav-bdg, .nav-tipx { display: none; }
  @media (pointer: coarse) { .nav-item { min-height: 44px; } }
</style>
