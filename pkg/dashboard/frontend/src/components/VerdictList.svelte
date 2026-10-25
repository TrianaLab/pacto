<script>
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import { miraLink, verdictBadge } from '../lib/delivery.ts';

  // Verdicts with the proof behind each: the citation, a link into Mira for a
  // health check when Mira's address is known (else the query to copy) and the
  // command that runs the check again.
  let { verdicts = [], miraURL = '', testid = 'verdicts' } = $props();

  // Keys shown elsewhere in the row, or as their own control.
  const SHOWN = new Set(['query', 'errorQuery']);
</script>

{#if verdicts.length === 0}
  <p class="t-body-2 vl-none">No checks yet.</p>
{:else}
  <ul class="vl-list" data-testid={testid}>
    {#each verdicts as v, i (v.gate + v.phase + i)}
      {@const b = verdictBadge(v)}
      <li class="vl-item">
        <div class="vl-head">
          <span class="badge {b.tone}" data-testid="verdict-badge"><span class="badge-dot"></span>{b.label}</span>
          {#if b.chip}<span class="badge badge-warn" data-testid="verdict-chip">{b.chip}</span>{/if}
          <strong>{v.gate}</strong>
          <span class="t-meta">{v.phase === 'pre' ? 'before the change' : v.phase === 'verify' ? 'after the change' : v.phase}</span>
        </div>
        {#if v.message}<p class="vl-msg t-body-2">{v.message}</p>{/if}
        {#if v.citation && Object.keys(v.citation).length}
          <dl class="vl-cite t-body-2">
            {#each Object.entries(v.citation).filter(([k]) => !SHOWN.has(k)) as [k, val] (k)}
              <dt>{k}</dt><dd><code>{val}</code></dd>
            {/each}
          </dl>
          {#if v.citation.query}
            {@const href = miraLink(v.citation, miraURL)}
            <p class="vl-proof t-body-2">
              {#if href}
                <a {href} target="_blank" rel="noopener noreferrer" data-testid="mira-link">Open the spans in Mira</a>
                {#if v.citation.from}<span class="t-meta">(window {v.citation.from} to {v.citation.to})</span>{/if}
              {:else}
                <span>Mira query:</span> <CopyableIdentifier value={v.citation.query} label="Mira query" />
              {/if}
            </p>
          {/if}
        {/if}
        {#if v.rerun}
          <p class="vl-proof t-body-2"><span>Run it again:</span> <CopyableIdentifier value={v.rerun} label="command" /></p>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

<style>
  .vl-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .vl-item { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .vl-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .vl-msg, .vl-proof { margin: 0; }
  .vl-proof a { color: var(--c-accent); text-decoration: underline; }
  .vl-cite { display: grid; grid-template-columns: max-content 1fr; gap: 2px var(--sp-3); margin: 0; }
  .vl-cite dt { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .vl-cite dd { margin: 0; overflow-wrap: anywhere; }
  .vl-none { color: var(--c-text-3); }
</style>
