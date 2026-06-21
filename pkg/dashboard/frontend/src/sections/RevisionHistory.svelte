<script>
  // Revision-history table for a readiness assessment. Renders nothing when there
  // are no revisions, so callers can include it unconditionally.
  let { revisions = [] } = $props();

  let hasRevisions = $derived((revisions?.length ?? 0) > 0);

  function fmtDate(d) {
    if (!d) return '—';
    const t = Date.parse(d);
    if (Number.isNaN(t)) return d;
    return new Date(t).toLocaleDateString();
  }
</script>

{#if hasRevisions}
  <div class="revision-history">
    <div class="revision-title">Revision history</div>
    <div class="table-wrap">
      <table class="revision-table">
        <thead>
          <tr>
            <th>Date</th>
            <th>Version</th>
            <th>Author</th>
            <th>Description</th>
          </tr>
        </thead>
        <tbody>
          {#each revisions as rev}
            <tr>
              <td class="text-2">{fmtDate(rev.date)}</td>
              <td>{#if rev.version}<code>{rev.version}</code>{:else}<span class="text-3">—</span>{/if}</td>
              <td>{#if rev.author}{rev.author}{:else}<span class="text-3">—</span>{/if}</td>
              <td class="revision-desc">{#if rev.description}{rev.description}{:else}<span class="text-3">—</span>{/if}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  </div>
{/if}

<style>
  .revision-history { margin-top: var(--sp-4); }
  .revision-title {
    font-size: var(--text-xs); font-weight: 600; text-transform: uppercase;
    letter-spacing: 0.05em; color: var(--c-text-3); margin-bottom: var(--sp-2);
  }
  .revision-table { font-size: var(--text-sm); width: 100%; }
  .revision-table th { font-size: var(--text-xs); }
  .revision-desc { color: var(--c-text-2); }
  .text-2 { color: var(--c-text-2); }
  .text-3 { color: var(--c-text-3); }
</style>
