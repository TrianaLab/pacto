<script lang="ts" module>
  import type { ProductAttentionList } from '../lib/api';
  export type RailItem = NonNullable<ProductAttentionList['items']>[number];
  /** The attention filters a deep link may carry. */
  export const RAIL_FILTERS = ['category', 'severity', 'status', 'owner', 'ownerKey', 'source', 'service', 'businessDomain', 'staleOnly'] as const;
</script>

<script lang="ts">
  /**
   * What needs attention, beside the fleet map: the attention list grouped by
   * severity, worst first, thirty at a time. An item about a service, revision
   * or target focuses that service on the map; any other item opens its page.
   * Filters arrive from the hash query (a #/fleet/attention deep link); the
   * filtered line names them with a Clear. Collapsed, the rail is one button
   * with the count.
   */
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { attentionCategoryLabel, severityLabel } from '../lib/entityLabels';
  import { declareTarget } from '../lib/declare';
  import NextActionLink from './NextActionLink.svelte';
  import { countOf, formatCount, statusLabel } from '../lib/format';
  import { SEVERITY_GLYPH } from '../lib/fleetMap/render';
  import { hashForHref } from '../lib/router';
  import { ownerKeyLabel, ownerKeyKind } from '../lib/owners';
  import { degradedSourceSummary, snapshotKnowledge } from '../lib/knowledgeState';

  const STEP = 30;
  const SEVERITIES = ['error', 'warning', 'info'] as const;
  const FILTER_WORDS: Record<string, string> = {
    category: 'Category', severity: 'Severity', status: 'Status', owner: 'Owner search', ownerKey: 'Owner',
    source: 'Source', service: 'Service', businessDomain: 'Domain', staleOnly: 'Stale only',
  };

  let {
    open = true,
    filters = {},
    current = null,
    ontoggle = () => {},
    onfocus = () => {},
    onclear = () => {},
  }: {
    open?: boolean;
    filters?: Record<string, string>;
    current?: RailItem | null;
    ontoggle?: (open: boolean) => void;
    onfocus?: (service: string, item: RailItem) => void;
    onclear?: () => void;
  } = $props();

  let limit = $state(STEP);
  let answer = $state.raw<{ key: string; list: ProductAttentionList } | null>(null);
  let error = $state<string | null>(null);
  let seq = 0;

  const active = $derived(RAIL_FILTERS.filter((k) => filters[k]).map((k) => [k, filters[k]] as const));
  const filterKey = $derived(active.map(([k, v]) => `${k}=${v}`).join('&'));

  // A new filter starts from the first thirty again.
  $effect(() => { void filterKey; untrack(() => { limit = STEP; }); });
  $effect(() => {
    const q = { ...Object.fromEntries(active), staleOnly: filters.staleOnly === '1' || undefined, limit };
    const mine = ++seq, key = filterKey;
    api.fleetAttention(q as never)
      .then((r) => { if (mine === seq) { answer = { key, list: r }; error = null; } })
      .catch((e: unknown) => { if (mine === seq) error = e instanceof Error ? e.message : String(e); });
  });

  // A list answers the filters it was asked with: under new filters the old one is never
  // shown, while Show more keeps it on screen until the longer list arrives.
  const list = $derived(answer?.key === filterKey ? answer.list : null);
  const items = $derived(list?.items ?? []);
  const total = $derived(list?.total ?? 0);
  const bySeverity = $derived(SEVERITIES.map((s) => [s, items.filter((i) => i.severity === s)] as const).filter(([, l]) => l.length));
  const counted = $derived((list?.severities?.errors ?? 0) + (list?.severities?.warnings ?? 0));
  // An empty list from an incomplete snapshot is no all-clear.
  const knowledge = $derived(snapshotKnowledge(list?.meta));

  const valueWords = (k: string, v: string) =>
    k === 'category' ? attentionCategoryLabel(v) : k === 'severity' ? severityLabel(v) : k === 'status' ? statusLabel(v) : k === 'staleOnly' ? 'yes'
      : k === 'ownerKey' && ownerKeyKind(v) ? `${ownerKeyLabel(v)} (${ownerKeyKind(v)})` : v;

  /** serviceOf is the service an item is about: itself, or the service of its revision or target. */
  function serviceOf(it: RailItem): string {
    if (it.entity.kind === 'service') return it.entity.key;
    if (it.entity.kind === 'revision' || it.entity.kind === 'target') return it.service || it.entity.parentService || '';
    return '';
  }

  function pick(it: RailItem) {
    const s = serviceOf(it);
    if (s) onfocus(s, it);
    else location.hash = it.entity.href;
  }

  const same = (a: RailItem | null, b: RailItem) => !!a && a.code === b.code && a.entity.key === b.entity.key && a.entity.kind === b.entity.kind;
</script>

{#if !open}
  <button type="button" class="rail-open" data-testid="fleet-map-rail-open" aria-expanded="false" onclick={() => ontoggle(true)}>
    Needs attention{list ? ` ${formatCount(counted)}` : ''}
  </button>
{:else}
  <aside class="rail" data-testid="fleet-map-rail" aria-label="Needs attention">
    <header class="rail-head">
      <h2>Needs attention{list ? ` (${formatCount(total)})` : ''}</h2>
      <button type="button" class="rail-close" aria-expanded="true" aria-label="Hide what needs attention" onclick={() => ontoggle(false)}>Hide</button>
    </header>
    {#if active.length}
      <p class="rail-filtered" data-testid="fleet-map-rail-filtered">
        Filtered: {active.map(([k, v]) => `${FILTER_WORDS[k]} ${valueWords(k, v)}`).join(' · ')}
        · <button type="button" class="rail-link" onclick={onclear}>Clear</button>
      </p>
    {/if}
    <div class="rail-body">
      {#if error}
        <p class="rail-msg" role="alert">The attention list could not load: {error}</p>
      {:else if list && !items.length}
        {#if knowledge.incomplete}
          <p class="rail-msg" data-testid="fleet-map-rail-unknown">Nothing found{active.length ? ' with these filters' : ''}, but Pacto cannot tell for sure: {degradedSourceSummary(knowledge) || 'the snapshot is incomplete'}.</p>
        {:else}
          <p class="rail-msg">Nothing needs attention{active.length ? ' with these filters' : ''}.</p>
        {/if}
      {/if}
      {#each bySeverity as [sev, its] (sev)}
        <section aria-label={severityLabel(sev)}>
          <h3 class="t-label rail-sev rail-{sev}"><span aria-hidden="true">{SEVERITY_GLYPH[sev]}</span> {severityLabel(sev)}</h3>
          <ul>
            {#each its as it, i (`${it.entity.kind}|${it.entity.key}|${it.code}|${i}`)}
              <li>
                <button type="button" class="ritem" data-testid="fleet-map-rail-item" aria-current={same(current, it) ? 'true' : undefined} onclick={() => pick(it)}>
                  <span class="ritem-t"><bdi>{it.entity.label}</bdi> <span class="ritem-c">{attentionCategoryLabel(it.category)}</span></span>
                  <span class="ritem-s">{it.summary}{#if it.source}{' '}via {it.source}{/if}</span>
                  {#if it.nextStep}<span class="ritem-n">Next: {it.nextStep}</span>{/if}
                  <!-- Words inside the button; a declare link cannot nest in it, so it follows. -->
                  {#if it.nextAction && !declareTarget(it.nextAction, it.caller ?? it.entity)}<NextActionLink action={it.nextAction} cls="ritem-n" />{/if}
                </button>
                {#if it.nextAction && declareTarget(it.nextAction, it.caller ?? it.entity)}<p class="ritem-caller"><NextActionLink action={it.nextAction} subject={it.caller ?? it.entity} /></p>{/if}
                <!-- The caller with no contract opens its own (inferred) service page. -->
                {#if it.caller}<p class="ritem-caller" data-testid="attention-caller">from <a href={hashForHref(it.caller.href)}><bdi>{it.caller.label || it.caller.key}</bdi></a></p>{/if}
              </li>
            {/each}
          </ul>
        </section>
      {/each}
      {#if items.length < total}
        <button type="button" class="rail-more" onclick={() => { limit += STEP; }}>
          Show {formatCount(Math.min(STEP, total - items.length))} more
        </button>
        <p class="rail-msg">{countOf(items.length, 'item')} of {formatCount(total)} shown.</p>
      {/if}
    </div>
  </aside>
{/if}

<style>
  .rail-open { position: absolute; z-index: 2; inset-block-start: var(--sp-3); inset-inline-end: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text-2); padding: 4px 10px; min-block-size: 32px; cursor: pointer; font-size: var(--text-sm); }
  .rail { position: absolute; z-index: 2; inset-block: 0; inset-inline-end: 0; inline-size: min(20rem, 85%); display: flex; flex-direction: column; background: var(--c-surface); border-inline-start: 1px solid var(--c-border); }
  .rail-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); padding: var(--sp-2) var(--sp-3); border-block-end: 1px solid var(--c-border); }
  .rail-head h2 { margin: 0; font-size: var(--text-md, 1rem); }
  .rail-close, .rail-link, .rail-more { border: 0; background: none; padding: 0; color: var(--c-accent); cursor: pointer; font-size: var(--text-sm); }
  .rail-filtered { margin: 0; padding: var(--sp-2) var(--sp-3); font-size: var(--text-sm); color: var(--c-text-2); border-block-end: 1px solid var(--c-border); overflow-wrap: anywhere; }
  .rail-body { flex: 1; overflow-y: auto; padding: var(--sp-2) var(--sp-3); }
  .rail-body ul { list-style: none; margin: 0 0 var(--sp-3); padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .rail-sev { margin: var(--sp-2) 0 var(--sp-1); }
  .rail-error span { color: var(--c-err); }
  .rail-warning span { color: var(--c-warn); }
  .rail-info span { color: var(--c-info); }
  .ritem { display: flex; flex-direction: column; gap: 2px; inline-size: 100%; text-align: start; border: 1px solid transparent; border-radius: var(--radius-xs); background: none; padding: var(--sp-1) var(--sp-2); color: var(--c-text); cursor: pointer; font: inherit; font-size: var(--text-sm); overflow-wrap: anywhere; }
  .ritem:hover { background: var(--c-surface-hover); }
  .ritem[aria-current='true'] { border-color: var(--c-accent); background: var(--c-accent-bg); }
  .ritem-c, .ritem-s, .ritem :global(.ritem-n) { color: var(--c-text-2); font-size: var(--text-xs); }
  .ritem-caller { margin: 0; padding: 0 var(--sp-3) var(--sp-1); font-size: var(--text-xs); color: var(--c-text-2); overflow-wrap: anywhere; }
  .ritem-t bdi { font-weight: 600; }
  .rail-msg { margin: var(--sp-2) 0; font-size: var(--text-sm); color: var(--c-text-3); }
  @media (pointer: coarse) { .rail-open, .rail-close, .rail-link, .rail-more, .ritem { min-inline-size: 44px; min-block-size: 44px; } }
</style>
