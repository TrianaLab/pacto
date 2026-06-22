<script>
  import { setFilter } from '../lib/filters.svelte.ts';
  import { serviceUrl } from '../lib/router.ts';
  import { readinessBucket, readinessBucketLabel, readinessBucketClass } from '../lib/format.ts';
  import StatusBadge from './StatusBadge.svelte';
  import ComplianceScore from './ComplianceScore.svelte';
  import OwnerLink from './OwnerLink.svelte';
  import SourceDot from './SourceDot.svelte';
  import EmptyState from './EmptyState.svelte';

  // The FILTERED list of services
  let { services = [] } = $props();
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
          <th data-tip="Service contract name">Name</th>
          <th data-tip="Current contract version">Version</th>
          <th data-tip="Contract compliance status">Contract Status</th>
          <th data-tip="Contract compliance score (0–100%)">Compliance</th>
          <th data-tip="Readiness score and status">Readiness</th>
          <th data-tip="Number of services impacted if this one fails">Blast</th>
          <th data-tip="Validation checks passed / total">Checks</th>
          <th data-tip="Data source: k8s, oci, or local">Source</th>
        </tr>
      </thead>
      <tbody>
        {#each services as svc}
          <tr class="clickable" onclick={() => (location.hash = serviceUrl(svc.name))}>
            <td>
              <a href={serviceUrl(svc.name)} class="svc-name">{svc.name}</a>
              <span class="svc-owner"><OwnerLink owner={svc.owner} /></span>
            </td>
            <td>
              <span class="pill">{svc.version || '—'}</span>
              {#if svc.updateAvailable}
                <span class="update-dot" data-tip="Newer version available"></span>
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
                <ComplianceScore score={svc.readiness.score} />
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
                >
                  {svc.blastRadius}
                </span>
              {:else}
                <span class="blast-badge blast-zero" data-tip="No services impacted if this one fails">0</span>
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
{/if}

<style>
  /* overflow-x:auto is only a last-resort fallback; the fixed layout below sizes
     the table to exactly the container width so a scrollbar never appears unless
     the viewport is genuinely too narrow. */
  .table-wrap {
    overflow-x: auto;
  }

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
  .col-status { width: 13%; }
  .col-compliance { width: 11%; }
  .col-readiness { width: 11%; }
  .col-blast { width: 8%; }
  .col-checks { width: 9%; }
  .col-source { width: 9%; }

  th, td {
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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

  .svc-name:hover {
    text-decoration: underline;
  }

  .svc-owner {
    margin-left: 6px;
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

  .blast-low {
    background: var(--c-warn-bg);
    color: var(--c-warn);
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
