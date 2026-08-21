<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { decideViewState, snapshotKnowledge } from '../lib/knowledgeState.ts';
  import {
    attentionCategoryLabel, ATTENTION_CATEGORIES, severityLabel,
  } from '../lib/entityLabels.ts';
  import { statusLabel, STATUS_FILTER_OPTIONS, ownerKeyLabel, ownerKeyKind } from '../lib/format.ts';
  import { fleetOverviewUrl, fleetAttentionUrl } from '../lib/router.ts';
  import Breadcrumbs from '../components/Breadcrumbs.svelte';
  import KnowledgeBanner from '../components/KnowledgeBanner.svelte';
  import EntityLink from '../components/EntityLink.svelte';
  import SeverityBadge from '../components/SeverityBadge.svelte';
  import ProductEmptyState from '../components/ProductEmptyState.svelte';
  import StaleRefreshNotice from '../components/StaleRefreshNotice.svelte';
  import ActiveFilterChips from '../components/ActiveFilterChips.svelte';
  import DistributionBar from '../components/viz/DistributionBar.svelte';
  import HorizontalBars from '../components/viz/HorizontalBars.svelte';
  import { severitySegments } from '../lib/distributions.ts';
  import PageHeader from '../components/PageHeader.svelte';

  // The attention triage workspace. It consumes
  // /api/fleet/attention with the backend-supported product filters, real backend
  // pagination (limit/offset/total/nextOffset) and every active filter kept in the
  // URL, so a triage view is deep-linkable and back/forward-restorable. Each item
  // answers what is affected, why, how severe, what evidence/source supports it and
  // what to inspect next (the backend-provided nextStep, never invented remediation).
  // `owner` is free-text SEARCH over team, DRI and contacts; `ownerKey` is the exact
  // canonical owner IDENTITY that owner pages and per-owner rankings link with, so a
  // canonical backlog for `team-a` never quietly includes `team-a-platform`.
  let {
    category = '', severity = '', status = '', owner = '', ownerKey = '', source = '', service = '',
    staleOnly = '', offset = '', refreshTick = 0,
  } = $props();

  const PAGE_SIZE = 25;
  const CATEGORIES = ATTENTION_CATEGORIES;
  const SEVERITIES = ['error', 'warning', 'info'];
  const pageOffset = $derived(Math.max(0, Math.trunc(Number(offset) || 0)));
  const isStale = $derived(staleOnly === '1');
  // The name a reader reads, and — where two owners share one — which of them.
  const ownerBox = $derived(ownerKey ? ownerKeyLabel(ownerKey) : owner);
  const ownerChipValue = $derived(
    ownerKeyKind(ownerKey) ? `${ownerKeyLabel(ownerKey)} (${ownerKeyKind(ownerKey)})` : ownerKey,
  );
  const anyFilter = $derived(!!(category || severity || status || owner || ownerKey || source || service || isStale));

  // One reusable, race-safe loader: the fetcher reads current filters
  // at request time; sync(key) dedupes the initial load and the generation guard stops
  // an older response overwriting a newer route/filter/refresh. Reloads when any
  // filter, the page offset (all from the URL) or the refresh tick changes, so
  // back/forward and deep links restore the exact page.
  const loader = createProductLoader(() => api.fleetAttention({
    category: category || undefined,
    severity: severity || undefined,
    status: status || undefined,
    owner: owner || undefined,
    ownerKey: ownerKey || undefined,
    source: source || undefined,
    service: service || undefined,
    staleOnly: isStale ? true : undefined,
    offset: pageOffset || undefined,
    limit: PAGE_SIZE,
  }));
  // queryIdentity is the QUESTION (filters + page); refreshTick only re-asks it. Rows
  // are retained across a re-ask and never across a different question.
  const queryIdentity = $derived([category, severity, status, owner, ownerKey, source, service, staleOnly, pageOffset].join('@@'));
  $effect(() => { loader.sync(`${queryIdentity}@@${refreshTick}`, queryIdentity); });
  onDestroy(() => loader.destroy());
  function load() { loader.refresh(); }

  const list = $derived(loader.dataTag === queryIdentity ? loader.data : null);
  const loading = $derived(loader.loading);
  const error = $derived(loader.error);
  const knowledge = $derived(snapshotKnowledge(list?.meta));
  const count = $derived(list?.items?.length ?? 0);
  const state = $derived(decideViewState({ loading, error, itemCount: count, filtered: anyFilter, knowledge }));
  // A poll that failed over rows we can still show. decideViewState keeps the rows;
  // this is the half that keeps it honest, so a frozen list never reads as a live one.
  const refreshError = $derived(state.kind === 'ready' ? state.refreshError : null);

  // A filter change resets the offset to page 1; a patch value of '' clears a filter.
  function urlWith(patch, off = 0) {
    const stale = patch.staleOnly !== undefined ? patch.staleOnly : isStale;
    return fleetAttentionUrl({
      category: patch.category ?? category,
      severity: patch.severity ?? severity,
      status: patch.status ?? status,
      owner: patch.owner ?? owner,
      ownerKey: patch.ownerKey ?? ownerKey,
      source: patch.source ?? source,
      service: patch.service ?? service,
      staleOnly: stale || undefined,
      offset: off,
    });
  }
  function apply(patch) { location.hash = urlWith(patch); }
  function clearAll() { location.hash = fleetAttentionUrl(); }

  const total = $derived(list?.total ?? 0);
  const shownFrom = $derived(total === 0 ? 0 : (list?.offset ?? pageOffset) + 1);
  const shownTo = $derived((list?.offset ?? pageOffset) + count);
  const hasPrev = $derived((list?.offset ?? pageOffset) > 0);
  const hasNext = $derived(list?.nextOffset != null);
  const prevOffset = $derived(Math.max(0, (list?.offset ?? pageOffset) - PAGE_SIZE));

  const chips = $derived([
    category ? { key: 'category', label: 'Category', value: attentionCategoryLabel(category) } : null,
    severity ? { key: 'severity', label: 'Severity', value: severityLabel(severity) } : null,
    status ? { key: 'status', label: 'Status', value: statusLabel(status) } : null,
    ownerKey ? { key: 'ownerKey', label: 'Owner', value: ownerChipValue } : null,
    owner ? { key: 'owner', label: 'Owner search', value: owner } : null,
    source ? { key: 'source', label: 'Source', value: source } : null,
    // Scoping arrives by deep link from a service page's posture bars; it gets a chip
    // like every other filter so the user can see they are in one service's backlog
    // and widen to the fleet in one click, instead of silently reading a subset.
    service ? { key: 'service', label: 'Service', value: service } : null,
    isStale ? { key: 'staleOnly', label: 'Stale only', value: 'yes' } : null,
  ].filter(Boolean));
  // Category buckets come from the backend in canonical order with their zeros; the
  // tone mirrors the severity each category habitually carries, and each bar is a
  // filter link so the chart is a triage control, not a picture.
  const CATEGORY_TONE = {
    'non-compliant': 'err', invalid: 'err', unknown: 'warn', stale: 'warn',
    unresolved: 'warn', readiness: 'info',
  };
  const categoryBars = $derived((list?.categories ?? []).map((b) => ({
    label: attentionCategoryLabel(b.category),
    value: b.count ?? 0,
    tone: CATEGORY_TONE[b.category] ?? 'neutral',
    href: urlWith({ category: b.category }),
  })));

  function removeChip(key) { apply(key === 'staleOnly' ? { staleOnly: false } : { [key]: '' }); }
</script>

<div class="product-page">
  <Breadcrumbs trail={[{ label: 'Overview', href: fleetOverviewUrl() }, { label: 'Needs attention' }]} />
  <PageHeader title="Needs attention" count={list ? `${list.total} item${list.total === 1 ? '' : 's'}` : ''} />

  <!-- Primary triage filters; secondary ones live behind an advanced disclosure so the
       default surface stays simple. -->
  <div class="av-filters">
    <label class="av-field">
      <span>Severity</span>
      <select value={severity} aria-label="Filter by severity" onchange={(e) => apply({ severity: e.currentTarget.value })}>
        <option value="">Any severity</option>
        {#each SEVERITIES as s}<option value={s}>{severityLabel(s)}</option>{/each}
      </select>
    </label>
    <label class="av-field">
      <span>Category</span>
      <select value={category} aria-label="Filter by category" onchange={(e) => apply({ category: e.currentTarget.value })}>
        <option value="">Any category</option>
        {#each CATEGORIES as c}<option value={c}>{attentionCategoryLabel(c)}</option>{/each}
      </select>
    </label>
    <details class="av-advanced disclosure">
      <summary><span class="disclosure-caret" aria-hidden="true">&#9656;</span>Advanced filters</summary>
      <div class="av-adv-grid">
        <label class="av-field">
          <span>Status</span>
          <select value={status} aria-label="Filter by compliance status" onchange={(e) => apply({ status: e.currentTarget.value })}>
            <option value="">Any status</option>
            <!-- The wire enum is the option VALUE; the option TEXT is the same word the
                 badges use. "NonCompliant" in a picker above a row badged "Not compliant"
                 asks the user to believe those are two states. -->
            {#each STATUS_FILTER_OPTIONS as s}<option value={s}>{statusLabel(s)}</option>{/each}
          </select>
        </label>
        <label class="av-field">
          <span>Owner</span>
          <!-- Arriving here from an owner page carries the canonical `ownerKey`; the
               box shows that owner's NAME, because `team:alice` is a wire form and
               editing it would hand the reader a search for the encoding. -->
          <input type="text" value={ownerBox} placeholder="team or DRI" aria-label="Filter by owner" onchange={(e) => apply({ owner: e.currentTarget.value.trim(), ownerKey: '' })} />
        </label>
        <label class="av-field">
          <span>Source</span>
          <input type="text" value={source} placeholder="source id" aria-label="Filter by source" onchange={(e) => apply({ source: e.currentTarget.value.trim() })} />
        </label>
        <label class="av-check">
          <input type="checkbox" checked={isStale} aria-label="Show only stale evidence" onchange={(e) => apply({ staleOnly: e.currentTarget.checked })} />
          <span>Stale evidence only</span>
        </label>
      </div>
    </details>
  </div>

  <ActiveFilterChips {chips} onRemove={removeChip} onClear={clearAll} />

  {#if knowledge.incomplete && (state.kind === 'ready' || state.kind === 'filtered-empty')}
    <KnowledgeBanner {knowledge} noun="attention list" />
  {/if}

  {#if refreshError}
    <StaleRefreshNotice noun="attention list" onRetry={load} />
  {/if}

  {#if state.kind !== 'ready'}
    <ProductEmptyState {state} noun="attention items" onRetry={load} onClearFilters={anyFilter ? clearAll : null} />
  {:else}
    <!-- The shape of the backlog before the backlog itself. Both charts read the
         backend tallies, which cover EVERY item the current filter matched -- not the
         page below them -- so the proportions do not change as the user pages. -->
    <div class="av-shape" data-testid="attention-shape">
      <DistributionBar
        title="By severity"
        level={2}
        description="How urgent the matched items are."
        segments={severitySegments(list.severities)}
        total={list.total}
      />
      <HorizontalBars
        title="By category"
        level={2}
        description="What kind of problem each matched item is."
        items={categoryBars}
        unit="items"
        unitOne="item"
      />
    </div>
    <ul class="attn-list" data-testid="attention-list">
      {#each list.items as it}
        <!-- Two columns, not one wrapping row. With everything in a single flex line
             the right-aligned next step dropped onto a second line as soon as the
             service name was long, so identical items rendered at two different
             heights and the list read as two kinds of thing. -->
        <li class="attn-item">
          <div class="attn-main">
            <SeverityBadge severity={it.severity} />
            <span class="attn-cat">{attentionCategoryLabel(it.category)}</span>
            <EntityLink ref={it.entity} showStatus={false} />
            <span class="attn-summary">{it.summary || it.reason || it.label}</span>
            {#if it.source}<span class="attn-src">via {it.source}</span>{/if}
          </div>
          {#if it.nextStep}<span class="attn-next"><span class="attn-next-k">Next</span>{it.nextStep}</span>{/if}
        </li>
      {/each}
    </ul>

    <nav class="av-pager" aria-label="Attention pages">
      <span class="av-range">Showing {shownFrom}–{shownTo} of {total}</span>
      <div class="av-pager-btns">
        {#if hasPrev}
          <a class="av-page" href={urlWith({}, prevOffset)} data-testid="attn-prev" rel="prev">Previous</a>
        {:else}
          <span class="av-page disabled" aria-disabled="true">Previous</span>
        {/if}
        {#if hasNext}
          <a class="av-page" href={urlWith({}, list.nextOffset)} data-testid="attn-next" rel="next">Next</a>
        {:else}
          <span class="av-page disabled" aria-disabled="true">Next</span>
        {/if}
      </div>
    </nav>
  {/if}
</div>

<style>
  /* Side by side, and deliberately so. The severity distribution is three rows and the
     category ranking is six, so the left column ends in white space -- but stacking
     them costs ninety vertical pixels, which is the difference between arriving at this
     page with triage items on screen and arriving at two charts. An empty corner hides
     nothing; a fold that starts below the first item does. */
  .av-shape { display: grid; grid-template-columns: repeat(auto-fit, minmax(min(100%, 18rem), 1fr)); gap: var(--sp-4); margin-bottom: var(--sp-4); align-items: start; }
  .av-filters { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; }
  .av-field { display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
  .av-filters select, .av-filters input[type="text"] {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface); color: var(--c-text); font: inherit; font-size: var(--text-sm); min-height: var(--touch-min);
  }
  /* Look and behaviour come from the shared .disclosure class: an accent-coloured
     summary here and quiet grey ones elsewhere read as two different controls. */
  .av-adv-grid { display: flex; gap: var(--sp-3); flex-wrap: wrap; align-items: flex-end; margin-top: var(--sp-2); }
  .av-check { display: flex; align-items: center; gap: var(--sp-2); font-size: var(--text-sm); color: var(--c-text-2); }
  .attn-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .attn-item {
    display: grid; grid-template-columns: minmax(0, 1fr) auto; align-items: center; gap: var(--sp-2) var(--sp-3);
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    background: var(--c-surface);
  }
  .attn-main { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .attn-cat {
    font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.03em;
    color: var(--c-text-3); background: var(--c-surface-inset); padding: 1px 6px; border-radius: var(--radius-xs);
  }
  .attn-summary { color: var(--c-text-2); font-size: var(--text-sm); }
  .attn-src { color: var(--c-text-3); font-size: var(--text-xs); }
  /* Backend-provided guidance, not a control. Right-aligned grey micro-text with an
     imperative verb read as a link that did nothing when clicked; the "Next" key
     names it as advice, the same way every labelled fact row does. */
  .attn-next { display: flex; align-items: baseline; gap: var(--sp-2); color: var(--c-text-3); font-size: var(--text-xs); text-align: right; }
  /* No opacity: every other uppercase micro-label in the product (.attn-cat, .te-k,
     .src-k) is plain --c-text-3, and dimming this one further put it at 3.58:1 -- below
     WCAG AA -- while also making it the only micro-label with its own shade. */
  .attn-next-k { text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .av-pager { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-2); }
  .av-range { color: var(--c-text-3); font-size: var(--text-sm); }
  .av-pager-btns { display: flex; gap: var(--sp-2); }
  .av-page {
    padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    font-size: var(--text-sm); color: var(--c-text); text-decoration: none; background: var(--c-surface);
    min-height: var(--touch-min); display: inline-flex; align-items: center;
  }
  .av-page:hover { border-color: var(--c-accent); text-decoration: none; }
  .av-page.disabled { color: var(--c-text-3); opacity: 0.5; pointer-events: none; }
</style>
