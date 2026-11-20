<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { createProductLoader } from '../lib/productLoader.svelte.ts';
  import { sourceHealthLabel, sourceHealthTone } from '../lib/entityLabels.ts';
  import { fleetEntityUrl, fleetSourcesUrl } from '../lib/router.ts';
  import { cap } from '../lib/cap.svelte.ts';
  import { formatCount } from '../lib/format.ts';
  import { formatRelative } from '../lib/dateFormat.ts';
  import { hashParam, setHashParams } from '../lib/hashQuery.svelte.ts';
  import ShowAll from './ShowAll.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import StaleRefreshNotice from './StaleRefreshNotice.svelte';

  // What Pacto found in the clusters it reads (509-S2): per row the provider, the
  // evidence, what Pacto uses it for and its status. A degraded row says why behind a
  // closed disclosure. A cluster Pacto could not read is "not read", never "nothing
  // detected": the empty sentence appears only when the server sent no row (hidden
  // inferred rows count) and no Kubernetes source is configured. The Kubernetes facts come from the answer, counted over every
  // source, never from the meta's bounded source list.
  let { refreshTick = 0 } = $props();

  const loader = createProductLoader(() => api.fleetProviders());
  $effect(() => { loader.sync(`providers@@${refreshTick}`, 'providers'); });
  onDestroy(() => loader.destroy());

  const NOT_READ_LINES = 3;
  const all = $derived(loader.data?.providers ?? []);
  // Rows the operator inferred (510-S5) are muted, carry one provenance mark and can be
  // hidden with one toggle kept in the URL as inferred=0; on is the default.
  const isInferred = (r) => r.provenance === 'inferred';
  const inferredCount = $derived(all.filter(isInferred).length);
  const showInferred = $derived(hashParam('inferred') !== '0');
  const rows = $derived(showInferred ? all : all.filter((r) => !isInferred(r)));
  // The row status is the completeness the operator reported, then its age.
  const COMPLETENESS = { available: 'complete', partial: 'partial', unavailable: 'unknown', stale: 'stale' };
  // The tip ends with the row's evidence, so a muted row is read in full where it is marked.
  const markText = (r) => [`Inferred from ${r.source}`, r.asOf && `as of ${formatRelative(r.asOf)}`, COMPLETENESS[r.status] ?? r.status].filter(Boolean).join(', ') + '. Nothing was turned on.' + (r.evidence ? ` ${r.evidence}` : '');
  // The operator's Installation row (510-S6) is declared configuration: the same discreet mark, never muted.
  const isDeclared = (r) => r.provenance === 'declared';
  const declaredText = (r) => ["Declared by the operator's configuration", r.asOf && `as of ${formatRelative(r.asOf)}`, COMPLETENESS[r.status] ?? r.status].filter(Boolean).join(', ') + '.';
  const notRead = $derived(loader.data?.notRead ?? []);
  const notReadTotal = $derived(loader.data?.notReadTotal ?? notRead.length);
  const clustered = $derived(new Set(rows.map((r) => r.cluster).filter(Boolean)).size > 1);
  const list = cap(5);
  const words = (t) => t.replaceAll('-', ' ');
  const why = (r) => r.status !== 'available' && r.limitations?.length;
</script>

<section class="dp" data-testid="detected-providers" aria-labelledby="dp-title">
  <div class="dp-head">
    <h2 id="dp-title" class="t-subsection-title">Detected providers</h2>
    {#if inferredCount}
      <label class="dp-toggle t-meta" data-testid="providers-inferred-toggle">
        <input type="checkbox" checked={showInferred} onchange={(e) => setHashParams({ inferred: e.currentTarget.checked ? null : '0' })} />
        Inferred
      </label>
    {/if}
  </div>
  {#if !loader.data && loader.error}
    <p class="t-meta" role="alert">Could not load the detected providers. <button type="button" class="btn btn-sm" onclick={() => loader.refresh()}>Retry</button></p>
  {:else if !loader.data}
    <div class="skeleton skeleton-line" aria-hidden="true"></div>
    <div class="skeleton skeleton-line" aria-hidden="true"></div>
  {:else if !all.length && !loader.data.kubernetesSources}
    <p class="t-meta">No providers detected. This view reads no Kubernetes cluster.</p>
  {:else}
    {#if loader.error}<StaleRefreshNotice noun="detected provider list" onRetry={() => loader.refresh()} />{/if}
    {#if rows.length}
      <ul class="dp-list">
        {#each list.slice(rows) as r (`${r.source}/${r.id}`)}
          <li class="dp-row" class:dp-inferred={isInferred(r)} data-testid="provider-row">
            <span class="dp-name">
              <span class="dp-dot tone-{sourceHealthTone(r.status)}" aria-hidden="true"></span>
              <span class:visually-hidden={r.status === 'available'} class="dp-status">{sourceHealthLabel(r.status)}</span>
              {#if clustered && r.cluster}<bdi class="dp-cluster" title={r.cluster}>{r.cluster}</bdi>{/if}
              <bdi class="dp-trunc" title={r.name}>{r.name}</bdi>
              {#if isInferred(r)}<ProvenanceMark label={`Inferred: from ${r.source}`} text={markText(r)} />{:else if isDeclared(r)}<ProvenanceMark label="Declared: by the operator's configuration" text={declaredText(r)} />{/if}
            </span>
            <bdi class="dp-trunc dp-evidence" title={r.evidence}>{r.evidence}</bdi>
            <span class="dp-used">{(r.usedFor ?? []).map(words).join(', ')}</span>
            {#if isInferred(r) || r.details?.length}
              {#if r.hint || r.details?.length || why(r)}
                <details class="disclosure dp-why">
                  <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>More details</summary>
                  {#if why(r)}<ul class="t-meta">{#each r.limitations as l}<li>{l.message}</li>{/each}</ul>{/if}
                  {#if r.details?.length}<ul class="t-meta">{#each r.details as d}<li><bdi>{d}</bdi></li>{/each}</ul>{/if}
                  {#if r.hint}<p class="dp-hint t-meta">Proposal to review: <CopyableIdentifier value={r.hint} label="command" /></p>{/if}
                </details>
              {/if}
            {:else if why(r)}
              <details class="disclosure dp-why">
                <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Why {sourceHealthLabel(r.status).toLowerCase()}</summary>
                <ul class="t-meta">{#each r.limitations as l}<li>{l.message}</li>{/each}</ul>
              </details>
            {/if}
          </li>
        {/each}
      </ul>
      <ShowAll cap={list} total={rows.length} noun="providers" testid="providers-show-all" />
    {/if}
    {#if !showInferred && inferredCount}
      <p class="t-meta" data-testid="providers-inferred-hidden">{formatCount(inferredCount)} inferred provider{inferredCount === 1 ? '' : 's'} hidden.</p>
    {/if}
    {#if all.length && loader.data.truncated}
      <p class="t-meta" data-testid="providers-truncated">Showing {formatCount(all.length)} of {formatCount(loader.data.total)} detected providers, reported before inferred, least healthy first.</p>
    {/if}
    {#if notRead.length}
      <ul class="dp-unread">
        {#each notRead.slice(0, NOT_READ_LINES) as s (s.id)}
          <li data-testid="provider-not-read" title={`${s.id} not read${s.error?.message ? ` (${s.error.message})` : ''}`}><a href={fleetEntityUrl('source', s.id)}><bdi>{s.id}</bdi></a> not read{#if s.error?.message} (<bdi>{s.error.message}</bdi>){/if}</li>
        {/each}
        {#if notReadTotal > NOT_READ_LINES}
          <li><a href={fleetSourcesUrl({ sourceHealth: 'unavailable' })} data-testid="providers-not-read-more">+{formatCount(notReadTotal - NOT_READ_LINES)} more not read</a></li>
        {/if}
      </ul>
    {/if}
  {/if}
</section>

<style>
  .dp-head { display: flex; flex-wrap: wrap; align-items: baseline; justify-content: space-between; gap: var(--sp-1) var(--sp-3); }
  .dp-toggle { display: inline-flex; align-items: center; gap: var(--sp-1); cursor: pointer; }
  @media (pointer: coarse) { .dp-toggle { min-block-size: 44px; } }
  /* Inferred rows read quieter than what a source reported directly. */
  .dp-inferred .dp-name { font-weight: 500; }
  .dp-inferred .dp-evidence, .dp-inferred .dp-used { color: var(--c-text-3); }
  .dp-hint { margin: var(--sp-1) 0 0; overflow-wrap: anywhere; }
  .dp { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  /* A stacked block per row on a narrow container, three columns on a wide one
     (SPEC 24.2): the container decides, never the viewport. */
  .dp-list { container-type: inline-size; list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
  .dp-row { position: relative; display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  /* The provenance tip opens under the row from its inline start: in the wide layout the
     mark sits mid-row, where a tip grown leftward from it would leave the viewport. */
  .dp-row .dp-name :global(.help) { position: static; }
  .dp-row .dp-name :global(.help-body) { left: 0; right: auto; max-width: min(38ch, 100%); }
  @container (min-width: 48rem) {
    .dp-row { grid-template-columns: minmax(18ch, 1fr) minmax(24ch, 2fr) minmax(16ch, 1fr); align-items: baseline; }
    .dp-why { grid-column: 1 / -1; }
  }
  /* The name keeps at least 12ch: past that the cluster wraps under it rather than
     crowding it out, and either truncates only at the full line width. */
  .dp-name { display: flex; flex-wrap: wrap; align-items: baseline; gap: 0 var(--sp-2); min-inline-size: 0; font-weight: 600; }
  .dp-dot { flex: none; inline-size: 8px; block-size: 8px; border-radius: 50%; background: var(--tone-c, var(--c-neutral)); align-self: center; }
  .dp-status { flex: none; color: var(--tone-c, var(--c-text-3)); font-weight: 500; font-size: var(--text-sm); }
  .dp-name > .dp-trunc { flex: 1 1 12ch; }
  .dp-cluster { flex: 0 1 auto; max-inline-size: 100%; color: var(--c-text-3); font-weight: 400; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dp-trunc { min-inline-size: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .dp-evidence { color: var(--c-text-2); }
  .dp-used { color: var(--c-text-2); font-size: var(--text-sm); overflow-wrap: anywhere; min-inline-size: 0; }
  .dp-why ul { margin: var(--sp-1) 0 0; padding-inline-start: var(--sp-4); overflow-wrap: anywhere; }
  /* Muted like .t-meta, which the list does not carry: the standalone "+N more" link
     must keep the 44px touch target that .t-meta exempts. */
  .dp-unread { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 2px; font-size: var(--text-xs); color: var(--c-text-3); }
  .dp-unread li { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .tone-err { --tone-c: var(--c-err); }
  .tone-neutral { --tone-c: var(--c-neutral); }
</style>
