<script>
  import { classificationClass, changeTypeClass, formatDiffValue, breakableIdentifierHtml } from './lib/format.ts';

  let { changes = [], compact = false } = $props();

  const COLLAPSE_THRESHOLD = 80;

  let expanded = $state({});

  function toggleExpand(idx) {
    expanded = { ...expanded, [idx]: !expanded[idx] };
  }

  // A reason that only says the path and the change again ("service.version
  // modified") repeats the row; it shows only when it adds something.
  function addsReason(change) {
    const r = (change.reason || '').trim().toLowerCase();
    return !!r && r !== `${change.path} ${change.type}`.toLowerCase();
  }

  // Two hundred changes show the first fifty and offer the rest.
  const ROWS = 50;
  let showAll = $state(false);
  const shown = $derived(showAll ? changes : changes.slice(0, ROWS));

  function needsExpand(val) {
    const text = formatDiffValue(val);
    return text.length > COLLAPSE_THRESHOLD || text.includes('\n');
  }
</script>

{#if !changes?.length}
  <p class="text-2">No changes detected</p>
{:else}
  <div class="table-wrap">
    <table class="data-table diff-table" class:diff-table-compact={compact}>
      <colgroup>
        <col class="dc-path" />
        <col class="dc-change" />
        <col class="dc-old" />
        <col class="dc-new" />
        <col class="dc-impact" />
      </colgroup>
      <!-- No `data-tip` on these headers. A <th> takes no focus, so the shared tooltip
           never opened for a keyboard user, and it is suppressed outright on touch --
           and every one of the five said back the word already printed in the cell
           ("Change" / "Type of change"). A definition worth keeping goes in a HelpTip,
           which is a real button; a restatement goes away. -->
      <thead><tr><th>Path</th><th>Change</th><th>Old</th><th>New</th><th>Breaking</th></tr></thead>
      <tbody>
        {#each shown as change, idx (idx)}
          {@const oldText = formatDiffValue(change.oldValue)}
          {@const newText = formatDiffValue(change.newValue)}
          {@const canExpand = needsExpand(change.oldValue) || needsExpand(change.newValue)}
          {@const isExpanded = !!expanded[idx]}
          <tr>
            <td data-label="Path"><code>{@html breakableIdentifierHtml(change.path)}</code></td>
            <td data-label="Change"><span class={changeTypeClass(change.type)}>{change.type}</span></td>
            <td data-label="Old">
              <pre class="diff-value" class:diff-value-collapsed={canExpand && !isExpanded}>{@html breakableIdentifierHtml(oldText)}</pre>
              {#if canExpand}
                <button type="button" class="expand-toggle" onclick={() => toggleExpand(idx)}>
                  {isExpanded ? 'collapse' : 'expand'}
                </button>
              {/if}
            </td>
            <td data-label="New">
              <pre class="diff-value" class:diff-value-collapsed={canExpand && !isExpanded}>{@html breakableIdentifierHtml(newText)}</pre>
              {#if canExpand}
                <button type="button" class="expand-toggle" onclick={() => toggleExpand(idx)}>
                  {isExpanded ? 'collapse' : 'expand'}
                </button>
              {/if}
            </td>
            <td data-label="Breaking">
              <span class="badge {classificationClass(change.classification)}">{change.classification.replace(/_/g, ' ')}</span>
              {#if addsReason(change)}<br><span class="text-3 diff-reason" style="font-size:var(--text-xs)">{@html breakableIdentifierHtml(change.reason)}</span>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  {#if changes.length > ROWS}
    <button type="button" class="btn btn-sm" aria-expanded={showAll} onclick={() => { showAll = !showAll; }} data-testid="diff-show-all">
      {showAll ? 'Show fewer' : `Show all ${changes.length.toLocaleString('en-US')} changes`}
    </button>
  {/if}
{/if}

<style>
  /* Fixed layout so the long <code> path and <pre> value cells can't over-grow
     the table and flash a spurious horizontal scrollbar. Path is left flexible
     to absorb %-rounding; Old/New share the remaining room and wrap their pre. */
  /* The shared data-table rules give it a fixed layout, wrapping cells and stacked
     cards on a phone; nowrap + overflow:visible cells painted "removed" over Old. */
  .diff-reason { display: inline-block; overflow-wrap: break-word; word-break: normal; }
  /* PATH gets the most room (it holds long dotted identifiers); Old/New hold
     short scalar values (long ones expand), so they don't need to dominate. This
     keeps most paths on one line and the rest to a clean 2-line boundary wrap. */
  .dc-path { width: auto; } /* ~34% of the remaining space */
  .dc-change { width: 8%; }
  .dc-old { width: 21%; }
  .dc-new { width: 21%; }
  .dc-impact { width: 18%; }
  /* Wrap long dotted paths at their natural boundaries (via <wbr> from
     breakableIdentifierHtml), never mid-word. break-word is only a last-resort
     fallback for a single segment longer than the column. */
  .diff-table td code { word-break: normal; overflow-wrap: break-word; }

  .diff-value {
    font-size: var(--text-xs);
    margin: 0;
    padding: 4px 6px;
    background: var(--c-surface);
    border-radius: var(--radius-xs);
    white-space: pre-wrap;
    /* Prefer the <wbr> boundary breaks from breakableIdentifierHtml; only break a
       word mid-character as a last resort (e.g. a single 60-char unbroken token). */
    word-break: normal;
    overflow-wrap: break-word;
  }
  .diff-value-collapsed {
    max-height: 3.2em;
    overflow: hidden;
    text-overflow: ellipsis;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
  }
  .diff-table-compact { font-size: var(--text-xs); }
  .expand-toggle {
    background: none; border: none; padding: 2px 0;
    font: inherit; font-size: var(--text-xs); font-weight: 500;
    color: var(--c-accent); cursor: pointer;
    margin-top: 2px; display: block;
    min-height: 28px;
  }
  .expand-toggle:hover { color: var(--c-accent-hover); text-decoration: underline; }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
</style>
