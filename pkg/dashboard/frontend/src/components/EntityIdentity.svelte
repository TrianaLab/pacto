<script>
  import { kindLabel } from '../lib/entityLabels.ts';
  import { abbreviateDigests } from '../lib/format.ts';
  import { identityContext, primaryLabel } from '../lib/identityContext.ts';
  import EntityStatusBadge from './EntityStatusBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  // Renders enough identity to DISAMBIGUATE an entity from same-named entities in
  // other domains/scopes: kind, human label, and the qualifying
  // context (domain / scope / parent service). `ref` is a product EntityRef.
  // showKind is turned off where a caption beside the link ALREADY names the kind
  // ("Owner" next to an OWNER chip). Two labels for one fact is how a page starts
  // reading like a form instead of a sentence.
  let { ref = {}, showStatus = true, showKey = false, showKind = true } = $props();

  const primary = $derived(primaryLabel(ref));

  // The disambiguating context bits, shared with the page header via
  // lib/identityContext so a page never disambiguates itself differently from the
  // row the user clicked to reach it.
  const context = $derived(identityContext(ref));

  // A content digest is 71 characters that identify content exactly and read as noise;
  // full width it also overflowed narrow cards and got clipped mid-string, which is
  // neither readable NOR complete. Show a recognizable prefix here, keep the exact value
  // in the tooltip, and leave the authoritative full digest on the revision page's
  // Identifier disclosure. Non-digest context (domain, scope, parent service) is short
  // and meaningful, so it comes back from the helper untouched.
  const display = (bit) => abbreviateDigests(bit);
</script>

<span class="entity-identity">
  {#if showKind}<span class="ei-kind">{kindLabel(ref.kind)}</span>{/if}
  <span class="ei-label">{primary}</span>
  {#if showStatus}<EntityStatusBadge kind={ref.kind} status={ref.status} />{/if}
  {#if context.length}
    <span class="ei-context">
      {#each context as bit, i}{#if i > 0}<span class="ei-dot" aria-hidden="true">·</span>{/if}<span title={display(bit) === bit ? undefined : bit}>{display(bit)}</span>{/each}
    </span>
  {/if}
</span>
{#if showKey && ref.key}
  <div class="ei-key"><CopyableIdentifier value={ref.key} label="canonical key" /></div>
{/if}

<style>
  .entity-identity { display: inline-flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .ei-kind {
    font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em;
    color: var(--c-text-3); background: var(--c-surface-inset);
    padding: 1px 6px; border-radius: var(--radius-xs); flex-shrink: 0;
  }
  .ei-label { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .ei-context { font-size: var(--text-xs); color: var(--c-text-3); display: inline-flex; gap: var(--sp-1); flex-wrap: wrap; min-width: 0; overflow-wrap: anywhere; }
  .ei-dot { color: var(--c-text-3); }
  .ei-key { margin-top: var(--sp-1); }
</style>
