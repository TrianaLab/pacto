<script>
  import { setFilter } from '../lib/filters.svelte.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { readinessBucket, readinessBucketLabel, readinessBucketClass, paginate, compareScoresUnassessedLast } from '../lib/format.ts';
  import StatusBadge from './StatusBadge.svelte';
  import ComplianceScore from './ComplianceScore.svelte';
  import ReadinessScore from './ReadinessScore.svelte';
  import OwnerLink from './OwnerLink.svelte';
  import SourceDot from './SourceDot.svelte';
  import EmptyState from './EmptyState.svelte';

  // The FILTERED list of services
  let { services = [] } = $props();

  const PER_PAGE = 25;
  // Worst-first rank so an ascending status sort surfaces the services that need
  // attention (NonCompliant) at the top.
  const STATUS_RANK = { NonCompliant: 0, Warning: 1, Unknown: 2, Reference: 3, Compliant: 4 };

  let sortBy = $state('blast');
  let sortAsc = $state(false); // blast descending by default (highest impact first)
  let page = $state(1);

  function setSort(col) {
    if (sortBy === col) sortAsc = !sortAsc;
    else { sortBy = col; sortAsc = col !== 'blast'; } // name/status/scores asc, blast desc
    page = 1;
  }
  function ariaSort(col) { return sortBy === col ? (sortAsc ? 'ascending' : 'descending') : 'none'; }
  function sortIcon(col) { return sortBy === col ? (sortAsc ? ' ↑' : ' ↓') : ''; }

  let sorted = $derived.by(() => {
    const dir = sortAsc ? 1 : -1;
    return [...services].sort((a, b) => {
      switch (sortBy) {
        case 'name': return (a.name || '').localeCompare(b.name || '') * dir;
        case 'status': return ((STATUS_RANK[a.contractStatus] ?? 5) - (STATUS_RANK[b.contractStatus] ?? 5)) * dir;
        case 'compliance': return compareScoresUnassessedLast(a.complianceScore ?? -1, b.complianceScore ?? -1, dir);
        case 'readiness': return compareScoresUnassessedLast(a.readiness?.score ?? -1, b.readiness?.score ?? -1, dir);
        case 'blast': return ((a.blastRadius || 0) - (b.blastRadius || 0)) * dir;
        default: return 0;
      }
    });
  });
  // paginate() clamps the page, so a filter that shrinks the list can't strand the
  // user past the end (and a poll refresh won't bounce them back to page 1).
  let paged = $derived(paginate(sorted, page, PER_PAGE));
</script>

{#if services.length === 0}
  <EmptyState title="No services" message="No services match the current filters." />
{:else}
  <div class="table-wrap">
    <table>
      <colgroup>
        <col class="col-name" />
        <col class="col-version" />
        <col class="col-status" />
        <col class="col-compliance" />
        <col class="col-readiness" />
        <col class="col-blast" />
        <col class="col-checks" />
        <col class="col-source" />
      </colgroup>
      <thead>
        <tr>
          <th aria-sort={ariaSort('name')}><button type="button" class="col-sort" data-tip="Service contract name" onclick={() => setSort('name')}>Name{sortIcon('name')}</button></th>
          <th data-tip="Current contract version">Version</th>
          <th aria-sort={ariaSort('status')}><button type="button" class="col-sort" data-tip="Contract compliance status" onclick={() => setSort('status')}>Contract Status{sortIcon('status')}</button></th>
          <th aria-sort={ariaSort('compliance')}><button type="button" class="col-sort" data-tip="Contract compliance score (0–100%)" onclick={() => setSort('compliance')}>Compliance{sortIcon('compliance')}</button></th>
          <th aria-sort={ariaSort('readiness')}><button type="button" class="col-sort" data-tip="Readiness score and status" onclick={() => setSort('readiness')}>Readiness{sortIcon('readiness')}</button></th>
          <th aria-sort={ariaSort('blast')}><button type="button" class="col-sort" data-tip="Number of services impacted if this one fails" onclick={() => setSort('blast')}>Blast{sortIcon('blast')}</button></th>
          <th data-tip="Validation checks passed / total">Checks</th>
          <th data-tip="Data source: k8s, oci, or local">Source</th>
        </tr>
      </thead>
      <tbody>
        {#each paged.items as svc}
          <tr class="clickable" onclick={() => (location.hash = serviceUrl(svc.name))}>
            <td>
              <a href={serviceUrl(svc.name)} class="svc-name">{svc.name}</a>
              <span class="svc-owner"><OwnerLink owner={svc.owner} /></span>
            </td>
            <td>
              <span class="pill">{svc.version || '—'}</span>
              {#if svc.updateAvailable}
                <span class="update-dot" data-tip="Newer version available" role="img" aria-label="Newer version available"></span>
              {/if}
            </td>
            <td>
              <button
                type="button"
                class="badge-btn"
                onclick={(e) => {
                  e.stopPropagation();
                  setFilter('contractStatus', svc.contractStatus);
                }}
              >
                <StatusBadge status={svc.contractStatus} />
              </button>
            </td>
            <td>
              <ComplianceScore score={svc.complianceScore} status={svc.complianceStatus} />
            </td>
            <td>
              {#if svc.readiness}
                <ReadinessScore readiness={svc.readiness} />
              {:else}
                <span class="text-dim">—</span>
              {/if}
            </td>
            <td>
              {#if svc.blastRadius > 0}
                <span
                  class="blast-badge"
                  class:blast-low={svc.blastRadius < 3}
                  class:blast-med={svc.blastRadius >= 3 && svc.blastRadius < 5}
                  class:blast-high={svc.blastRadius >= 5}
                  data-tip="{svc.blastRadius} service{svc.blastRadius !== 1 ? 's' : ''} impacted if this one fails"
                  aria-label="{svc.blastRadius} service{svc.blastRadius !== 1 ? 's' : ''} impacted if this one fails"
                >
                  {svc.blastRadius}
                </span>
              {:else}
                <span class="blast-badge blast-zero" data-tip="No services impacted if this one fails" aria-label="No services impacted if this one fails">0</span>
              {/if}
            </td>
            <td>
              {#if svc.checksTotal > 0}
                <span class:text-ok={svc.checksFailed === 0} class:text-err={svc.checksFailed > 0}>
                  {svc.checksPassed}/{svc.checksTotal}
                </span>
              {:else}
                <span class="text-dim">—</span>
              {/if}
              {#if svc.evaluationCoverage}
                <span
                  class="eval-badge"
                  data-tip="Required assertions evaluated at runtime — metadata, does not change status"
                  aria-label="Evaluation coverage {svc.evaluationCoverage.evaluated} of {svc.evaluationCoverage.required}"
                >cov {svc.evaluationCoverage.evaluated}/{svc.evaluationCoverage.required}</span>
              {/if}
            </td>
            <td>
              {#each svc.sources || [svc.source] as src}
                <SourceDot source={src} align="right" />
              {/each}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if paged.total > PER_PAGE}
    <div class="table-footer">
      <span class="showing">Showing {(paged.page - 1) * PER_PAGE + 1}–{Math.min(paged.page * PER_PAGE, paged.total)} of {paged.total}</span>
      <div class="pager">
        <button type="button" class="btn btn-sm" disabled={paged.page <= 1} onclick={() => page = paged.page - 1}>Prev</button>
        <span class="pager-pos">Page {paged.page} / {paged.totalPages}</span>
        <button type="button" class="btn btn-sm" disabled={paged.page >= paged.totalPages} onclick={() => page = paged.page + 1}>Next</button>
      </div>
    </div>
  {/if}
{/if}

<style>
  /* Sortable column header button — inherits the th typography, adds a pointer. */
  .col-sort {
    background: none; border: 0; padding: 0; margin: 0; cursor: pointer;
    font: inherit; color: inherit; text-transform: inherit; letter-spacing: inherit;
    display: inline-flex; align-items: center; white-space: nowrap;
  }
  .col-sort:hover { color: var(--c-text); }

  .table-footer {
    display: flex; align-items: center; justify-content: space-between;
    gap: var(--sp-3); flex-wrap: wrap; margin-top: var(--sp-3);
    font-size: var(--text-sm); color: var(--c-text-3);
  }
  .pager { display: flex; align-items: center; gap: var(--sp-2); }
  .pager-pos { font-variant-numeric: tabular-nums; }

  /* The .table-wrap div deliberately has NO scoped overflow rule: it inherits the
     global .table-wrap (overflow:visible on desktop, overflow-x:auto only ≤768px).
     A scoped overflow-x:auto here forced a permanent scroll context on desktop, and
     since cells are overflow:visible (so tooltips escape), the hover tooltips that
     paint past the last column turned into a spurious horizontal scrollbar. */

  /* Fixed layout: column widths come from <colgroup>, not from content's
     intrinsic min-content. This stops the nowrap columns from over-growing the
     table past 100% and triggering a spurious horizontal scrollbar. */
  table {
    width: 100%;
    box-sizing: border-box;
    table-layout: fixed;
  }

  /* Name column flexes and truncates; the rest are compact and fixed-ish. */
  .col-name { width: auto; }
  .col-version { width: 12%; }
  .col-status { width: 14%; }
  .col-compliance { width: 11%; }
  .col-readiness { width: 11%; }
  .col-blast { width: 8%; }
  .col-checks { width: 9%; }
  .col-source { width: 9%; }

  th {
    white-space: normal;
    overflow: visible;
    text-overflow: clip;
  }

  td {
    white-space: nowrap;
    overflow: visible;
  }

  th:first-child, td:first-child {
    white-space: normal;
  }

  .svc-name {
    font-weight: 600;
    text-decoration: none;
    display: block;
    max-width: 100%;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .svc-owner {
    margin-left: 6px;
  }

  .svc-name:hover {
    text-decoration: underline;
  }

  .text-dim {
    color: var(--c-text-3);
  }

  .text-ok {
    color: var(--c-ok);
  }

  .text-err {
    color: var(--c-err);
  }

  .blast-badge {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    min-width: 26px;
    height: 22px;
    padding: 0 7px;
    border-radius: var(--radius-xs);
    font-size: var(--text-xs);
    font-weight: 600;
  }

  /* 1-2 impacted is low: neutral, matching the >=3 "high impact" threshold used
     everywhere else — warn/err colors are reserved for >=3. */
  .blast-low {
    background: var(--c-neutral-bg);
    color: var(--c-text-2);
  }

  .blast-med {
    background: var(--c-warn-bg);
    color: var(--c-warn);
    border: 1px solid color-mix(in srgb, var(--c-warn) 25%, transparent);
  }

  .blast-high {
    background: var(--c-err-bg);
    color: var(--c-err);
    border: 1px solid color-mix(in srgb, var(--c-err) 25%, transparent);
  }

  .blast-zero {
    background: var(--c-neutral-bg);
    color: var(--c-text-3);
  }

  .eval-badge {
    display: inline-block;
    margin-left: 6px;
    padding: 1px 6px;
    border-radius: var(--radius-xs);
    background: var(--c-neutral-bg);
    color: var(--c-text-3);
    font-size: 10px;
    font-weight: 600;
    vertical-align: middle;
  }

  .update-dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--c-accent);
    margin-left: 4px;
    vertical-align: middle;
  }
</style>
