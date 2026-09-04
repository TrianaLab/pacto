<script>
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import MarkdownView from '../MarkdownView.svelte';
  import DocModal from '../DocModal.svelte';
  import RevisionHistory from './RevisionHistory.svelte';
  import { readinessGateClass, readinessGateTip, checkStatusClass, checkStatusLabel, assessmentCountdownLabel } from '../lib/format.ts';
  import { formatDate } from '../lib/dateFormat.ts';

  let { readiness = null, docs = [], open = $bindable(false), id = '', source = '' } = $props();

  let hasContent = $derived(!!readiness && (readiness.checks?.length ?? 0) > 0);
  let expanded = $state({});
  let modalDoc = $state(null);

  let countdown = $derived(readiness ? assessmentCountdownLabel(readiness.expired, readiness.daysRemaining) : '');

  function toggle(i) {
    expanded = { ...expanded, [i]: !expanded[i] };
  }

  function docFor(path) {
    return docs?.find((d) => d.path === path);
  }

  // Normalized contribution (%) so weights read consistently regardless of their absolute sum.
  function pct(weight) {
    const total = readiness?.totalWeight ?? 0;
    return total > 0 ? Math.round((weight * 100) / total) : 0;
  }
</script>

{#if hasContent}
  <CollapsibleSection title="Readiness" count={readiness.checks.length} bind:open {id} {source}>
    <div class="readiness-summary">
      <!-- Score colored by the GATE (passing), not the absolute value, with a ✓
           when it clears minScore — so passing vs below-gate is obvious. -->
      <div class="score {readinessGateClass(readiness)}" data-tip={readinessGateTip(readiness)}>
        {readiness.score}<span class="score-unit">%</span>{#if readiness.passing && !readiness.expired}<span class="gate-check" aria-label="passes minScore" title="passes minScore">&#10003;</span>{/if}
      </div>
      <div class="readiness-metrics">
        <div class="metric">
          <span class="metric-label">Gate</span>
          <span class="metric-value {readiness.passing ? 'gate-pass' : 'gate-fail'}" data-tip="score must be >= minScore">
            {readiness.passing ? 'PASS' : 'FAIL'} (≥ {readiness.minScore})
          </span>
        </div>
        <div class="metric">
          <span class="metric-label">Earned weight</span>
          <span class="metric-value">{readiness.earnedWeight} / {readiness.totalWeight}</span>
        </div>
        {#if readiness.partialCredit > 0}
          <div class="metric">
            <span class="metric-label">Partial credit</span>
            <span class="metric-value">{readiness.partialCredit}</span>
          </div>
        {/if}
        {#if readiness.expires}
          <div class="metric">
            <span class="metric-label">Expires</span>
            <span class="metric-value" class:gate-fail={readiness.expired} data-tip={readiness.expires}>
              {formatDate(readiness.expires) || readiness.expires}
              {#if countdown}<span class="countdown" class:countdown-expired={readiness.expired}>{countdown}</span>{/if}
            </span>
          </div>
        {:else if readiness.expired}
          <div class="metric">
            <span class="metric-label">Assessment</span>
            <span class="metric-value gate-fail">Expired</span>
          </div>
        {/if}
      </div>
    </div>

    <div class="table-wrap">
      <table class="readiness-table">
        <colgroup>
          <col class="rt-check" />
          <col class="rt-type" />
          <col class="rt-cat" />
          <col class="rt-status" />
          <col class="rt-weight" />
          <col class="rt-earned" />
          <col class="rt-evidence" />
        </colgroup>
        <thead>
          <tr>
            <th>Check</th>
            <th>Type</th>
            <th>Category</th>
            <th>Status</th>
            <th>Weight</th>
            <th>Earned</th>
            <th>Evidence</th>
          </tr>
        </thead>
        <tbody>
          {#each readiness.checks as c, i}
            <tr>
              <td>
                <span class="check-id">{c.id}</span>
                {#if c.description}<div class="check-desc">{c.description}</div>{/if}
              </td>
              <td><span class="pill">{c.type}</span></td>
              <td>{#if c.category}<span class="pill pill-cat">{c.category}</span>{:else}<span class="text-3">—</span>{/if}</td>
              <td><span class="badge {checkStatusClass(c.status)}">{checkStatusLabel(c.status)}</span></td>
              <td>{c.weight} <span class="text-3">({pct(c.weight)}%)</span></td>
              <td class="text-2">{c.earnedWeight}</td>
              <td class="evidence-cell">
                {#if c.docPath && docFor(c.docPath)}
                  <button type="button" class="doc-toggle" class:open={expanded[i]} onclick={() => toggle(i)}>
                    <span class="doc-chevron" data-motion class:open={expanded[i]}>▸</span> view
                  </button>
                {:else if c.evidence}
                  <code>{c.evidence}</code>
                {:else}
                  <span class="text-3">—</span>
                {/if}
              </td>
            </tr>
            {#if c.docPath && expanded[i] && docFor(c.docPath)}
              <tr class="doc-expand-row">
                <td colspan="7">
                  <div class="doc-expand">
                    <div class="doc-expand-head">
                      <code class="doc-expand-path">{c.docPath}</code>
                      <button type="button" class="fullscreen-btn" title="Read full screen" aria-label="Read full screen"
                        onclick={() => { modalDoc = docFor(c.docPath); }}>
                        <svg viewBox="0 0 14 14" fill="none" aria-hidden="true"><path d="M2 5V2h3M12 5V2H9M2 9v3h3M12 9v3H9" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"/></svg>
                      </button>
                    </div>
                    <MarkdownView content={docFor(c.docPath).content} truncated={docFor(c.docPath).truncated} />
                  </div>
                </td>
              </tr>
            {/if}
          {/each}
        </tbody>
      </table>
    </div>

    <RevisionHistory revisions={readiness.revisions || []} />
  </CollapsibleSection>
  <DocModal doc={modalDoc} onClose={() => { modalDoc = null; }} />
{/if}

<style>
  .readiness-summary {
    display: flex;
    align-items: center;
    gap: var(--sp-5);
    margin-bottom: var(--sp-4);
    flex-wrap: wrap;
  }
  .score {
    font-size: var(--text-2xl, 1.5rem);
    font-weight: 700;
    line-height: 1;
  }
  .score-unit { font-size: var(--text-sm); font-weight: 500; color: var(--c-text-3); }
  .gate-check { font-size: 0.7em; font-weight: 700; margin-left: 4px; color: var(--c-ok); vertical-align: super; }
  .readiness-metrics {
    display: flex;
    gap: var(--sp-5);
    flex-wrap: wrap;
  }
  .metric { display: flex; flex-direction: column; gap: 2px; }
  .metric-label { font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.03em; }
  .metric-value { font-size: var(--text-sm); font-weight: 600; color: var(--c-text); }
  .countdown { font-weight: 500; color: var(--c-text-3); margin-left: 6px; }
  .countdown-expired { color: var(--c-err); }

  /* Fixed layout sizes columns from <colgroup>, never from content min-content,
     so the nowrap data cells can't over-grow the table past 100% and flash a
     spurious horizontal scrollbar. The Evidence column is left flexible to
     absorb %-rounding so the table is exactly its container width. */
  .readiness-table { font-size: var(--text-sm); width: 100%; table-layout: fixed; }
  .readiness-table th { font-size: var(--text-xs); white-space: normal; }
  .readiness-table td {
    overflow: visible;
    white-space: nowrap;
  }
  .rt-check { width: 22%; }
  .rt-type { width: 10%; }
  .rt-cat { width: 12%; }
  .rt-status { width: 12%; }
  .rt-weight { width: 12%; }
  .rt-earned { width: 9%; }
  .rt-evidence { width: auto; }
  /* Check + Evidence carry multi-line content; let them wrap rather than clip. */
  .readiness-table td:first-child,
  .readiness-table .evidence-cell { white-space: normal; overflow: visible; }
  .check-id { font-weight: 600; }
  .check-desc { font-size: var(--text-xs); color: var(--c-text-3); margin-top: 2px; }
  .pill-cat { background: var(--c-neutral-bg); color: var(--c-text-2); }
  .evidence-cell code { font-size: var(--text-xs); color: var(--c-text-2); word-break: break-all; }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); font-size: var(--text-xs); }
  .gate-pass { color: var(--c-ok, #2da44e); }
  .gate-fail { color: var(--c-err, #cf222e); }

  .doc-toggle {
    background: none;
    border: none;
    color: var(--c-accent);
    font: inherit;
    font-size: var(--text-xs);
    cursor: pointer;
    padding: 0;
    display: inline-flex;
    align-items: center;
    gap: 4px;
  }
  .doc-toggle:hover { text-decoration: underline; }
  .doc-chevron { display: inline-block; }
  .doc-chevron.open { transform: rotate(90deg); }
  .doc-expand-row > td { padding: 0 !important; }
  .doc-expand {
    padding: var(--sp-3) var(--sp-4);
    background: var(--c-surface-inset);
    border-top: 1px solid var(--c-border);
  }
  .doc-expand-head { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); margin-bottom: var(--sp-2); }
  .doc-expand-path { display: block; font-size: var(--text-xs); color: var(--c-text-3); }
  .fullscreen-btn {
    display: inline-flex; align-items: center; justify-content: center;
    width: 26px; height: 26px; flex-shrink: 0;
    background: none;
    border: 1px solid var(--c-border);
    border-radius: var(--radius-xs);
    color: var(--c-text-3);
    cursor: pointer;
  }
  .fullscreen-btn:hover { background: var(--c-surface-hover, var(--c-surface-inset)); color: var(--c-text); }
  .fullscreen-btn svg { width: 14px; height: 14px; }

  @media (max-width: 768px) {
    .readiness-summary { gap: var(--sp-3); }
    .readiness-metrics { gap: var(--sp-3); }
  }
</style>
