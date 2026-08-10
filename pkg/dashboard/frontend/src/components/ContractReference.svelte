<script>
  import EntityLink from './EntityLink.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';

  // One contract-to-contract reference, rendered once for every surface that shows
  // one (configuration scopes, policies).
  //
  // The AUTHORED ref is contract information and always stays visible verbatim -- it
  // is what the revision actually declared, and no resolution replaces it. What the
  // BACKEND resolved that ref to arrives as a canonical entity ref, so the destination
  // is navigable; it is never inferred here from the ref string or from a display
  // label. A reference the backend could not resolve says so, with the reason it gave,
  // and fabricates no service.
  let { value = '', resolution = null } = $props();
</script>

<span class="cr">
  <CopyableIdentifier {value} label="reference" />
  {#if resolution?.service}
    <span class="cr-arrow" aria-hidden="true">&rarr;</span>
    <EntityLink ref={resolution.service} showStatus={false} showKind={false} />
  {:else if resolution && !resolution.resolved}
    <span class="cr-unresolved">Unresolved{resolution.reason ? ` — ${resolution.reason}` : ''}</span>
  {/if}
</span>

<style>
  .cr { display: inline-flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .cr-arrow { color: var(--c-text-3); }
  .cr-unresolved { font-size: var(--text-xs); color: var(--c-warn); }
</style>
