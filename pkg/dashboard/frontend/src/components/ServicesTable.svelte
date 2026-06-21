<script>
  import { setFilter } from '../lib/filters.svelte.ts';
  import { serviceUrl, ownerUrl } from '../lib/router.ts';
  import {
    statusClass,
    complianceClass,
    complianceStatusClass,
    sourceTooltip,
    ownerDisplay,
    ownerKey,
    readinessBucket,
    readinessBucketLabel,
    readinessBucketClass,
  } from '../lib/format.ts';

  // The FILTERED list of services
  let { services = [] } = $props();

  const STATUS_LABELS = {
    Compliant: 'Compliant',
    Warning: 'Warning',
    NonCompliant: 'Non-Compliant',
    Unknown: 'Unknown',
    Reference: 'Reference',
  };

  function statusLabel(s) {
    return STATUS_LABELS[s] || s;
  }
</script>

{#if services.length === 0}
  <div class="state-box">
    <h3>No services</h3>
    <p>No services match the current filters.</p>
  </div>
{:else}
  <div class="table-wrap">
    <table>
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
              {#if ownerDisplay(svc.owner)}
                <a
                  href={ownerUrl(ownerKey(svc.owner))}
                  class="svc-owner"
                  onclick={(e) => {
                    e.stopPropagation();
                    setFilter('owner', ownerKey(svc.owner));
                  }}
                >
                  {ownerDisplay(svc.owner)}
                </a>
              {/if}
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
                <span class="badge badge-{statusClass(svc.contractStatus)}">
                  <span class="badge-dot"></span>{statusLabel(svc.contractStatus)}
                </span>
              </button>
            </td>
            <td>
              {#if svc.complianceScore != null}
                <span class="score {complianceStatusClass(svc.complianceStatus)}">{svc.complianceScore}%</span>
              {:else}
                <span class="text-dim">—</span>
              {/if}
            </td>
            <td>
              {#if svc.readiness}
                <span class="score {complianceClass(svc.readiness.score)}">{svc.readiness.score}<span class="score-unit">%</span></span>
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
                <span class="source-dot source-dot-{src}" data-tip={sourceTooltip(src)} data-tip-align="right"></span>
              {/each}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .state-box {
    padding: var(--sp-5);
    text-align: center;
    color: var(--c-text-3);
  }

  .table-wrap {
    overflow-x: auto;
  }

  table {
    width: 100%;
  }

  th, td {
    white-space: nowrap;
  }

  th:first-child, td:first-child {
    white-space: normal;
  }

  .svc-name {
    font-weight: 600;
    text-decoration: none;
    display: block;
    max-width: 200px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .svc-name:hover {
    text-decoration: underline;
  }

  .svc-owner {
    color: var(--c-text-3);
    font-size: var(--text-xs);
    margin-left: 6px;
    text-decoration: none;
  }

  .svc-owner:hover {
    color: var(--c-text-2);
    text-decoration: underline;
  }

  .badge-btn {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    cursor: pointer;
  }

  .score {
    font-weight: 600;
  }

  .score-unit {
    font-size: 0.8em;
    font-weight: 500;
    color: var(--c-text-3);
    margin-left: 1px;
  }

  .score.score-ok {
    color: var(--c-ok);
  }

  .score.score-warn {
    color: var(--c-warn);
  }

  .score.score-err {
    color: var(--c-err);
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
