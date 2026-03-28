<script>
  import { classificationClass, changeTypeClass, formatDiffValue } from './lib/format.ts';

  let { changes = [], compact = false } = $props();

  const COLLAPSE_THRESHOLD = 80;

  let expanded = $state({});

  function toggleExpand(idx) {
    expanded = { ...expanded, [idx]: !expanded[idx] };
  }

  function needsExpand(val) {
    const text = formatDiffValue(val);
    return text.length > COLLAPSE_THRESHOLD || text.includes('\n');
  }
</script>

{#if changes.length === 0}
  <p class="text-2">No changes detected</p>
{:else}
  <div class="table-wrap">
    <table class:diff-table-compact={compact}>
      <thead><tr><th data-tip="Field path in the contract">Path</th><th data-tip="Type of change">Change</th><th data-tip="Value in the older version">Old</th><th data-tip="Value in the newer version">New</th><th data-tip="Breaking change classification">Impact</th></tr></thead>
      <tbody>
        {#each changes as change, idx}
          {@const oldText = formatDiffValue(change.oldValue)}
          {@const newText = formatDiffValue(change.newValue)}
          {@const canExpand = needsExpand(change.oldValue) || needsExpand(change.newValue)}
          {@const isExpanded = !!expanded[idx]}
          <tr>
            <td><code>{change.path}</code></td>
            <td><span class={changeTypeClass(change.type)}>{change.type}</span></td>
            <td>
              <pre class="diff-value" class:diff-value-collapsed={canExpand && !isExpanded}>{oldText}</pre>
              {#if canExpand}
                <button type="button" class="expand-toggle" onclick={() => toggleExpand(idx)}>
                  {isExpanded ? 'collapse' : 'expand'}
                </button>
              {/if}
            </td>
            <td>
              <pre class="diff-value" class:diff-value-collapsed={canExpand && !isExpanded}>{newText}</pre>
              {#if canExpand}
                <button type="button" class="expand-toggle" onclick={() => toggleExpand(idx)}>
                  {isExpanded ? 'collapse' : 'expand'}
                </button>
              {/if}
            </td>
            <td>
              <span class="badge {classificationClass(change.classification)}">{change.classification.replace(/_/g, ' ')}</span>
              {#if change.reason}<br><span class="text-3" style="font-size:var(--text-xs)">{change.reason}</span>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
{/if}

<style>
  .diff-value {
    font-size: var(--text-xs);
    margin: 0;
    padding: 2px 4px;
    background: var(--c-surface);
    border-radius: var(--radius-xs);
    white-space: pre-wrap;
    word-break: break-word;
  }
  .diff-value-collapsed {
    max-height: 2.8em;
    overflow: hidden;
    text-overflow: ellipsis;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
  }
  .diff-table-compact { font-size: var(--text-xs); }
  .expand-toggle {
    background: none; border: none; padding: 0;
    font: inherit; font-size: 10px; font-weight: 500;
    color: var(--c-accent); cursor: pointer;
    margin-top: 2px; display: block;
  }
  .expand-toggle:hover { color: var(--c-accent-hover); text-decoration: underline; }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
</style>
