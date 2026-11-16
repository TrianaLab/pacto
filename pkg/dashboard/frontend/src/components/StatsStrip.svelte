<script>
  // The stats strip (AC4): how sure a Metrics number is, said under every chart from the
  // answer's envelope. "1,204 records read, 3 skipped | Partial | as of 15 Nov, 10:00 |
  // unsigned statistics". The completeness badge is never complete when it is not.
  import { countOf, formatCount } from '../lib/format.ts';
  import { formatDateTime } from '../lib/dateFormat.ts';

  /** @type {{ st: { asOf: string, recordsRead: number, skipped?: { malformed: number, oversized: number, unreadable: number }, truncated?: string, completeness: string, provenance: string } }} */
  let { st } = $props();
  const skipped = $derived((st.skipped?.malformed ?? 0) + (st.skipped?.oversized ?? 0) + (st.skipped?.unreadable ?? 0));
  const badge = $derived(st.completeness === 'complete' ? 'Complete' : st.completeness === 'empty' ? 'No records' : 'Partial');
</script>

<p class="stats-strip t-meta" data-testid="stats-strip">
  <span>{countOf(st.recordsRead, 'record')} read{#if skipped}, {formatCount(skipped)} skipped{/if}{#if st.truncated}, cut at {st.truncated}{/if}</span>
  <span class="badge wrap {st.completeness === 'complete' ? 'badge-ok' : 'badge-neutral'}">{badge}</span>
  <span>as of {formatDateTime(st.asOf)}</span>
  <span class="stats-sep" aria-hidden="true"></span>
  <span>{st.provenance}</span>
</p>

<style>
  .stats-strip { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-1) var(--sp-3); margin: 0; }
  .stats-sep { inline-size: 1px; block-size: 14px; background: var(--c-border); }
</style>
