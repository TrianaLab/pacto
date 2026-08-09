<script>
  import { onMount } from 'svelte';
  import { api, ApiError, SchemaCompatibilityError } from '../lib/api.ts';
  import { replaceHash, hashForHref, fleetServicesUrl, fleetOwnersUrl } from '../lib/router.ts';
  import EntityLink from '../components/EntityLink.svelte';

  // Migrates a legacy name-bearing URL (#/services/:name, #/owners/:id) to its canonical
  // Product entity on a Fleet-capable host (Part 1). It resolves the display NAME through
  // the Product Entities API and NEVER fabricates a canonical key from the name:
  //   - exactly one match  -> replace the URL with its canonical Product route (a replace,
  //     not a push, so a reload stays on the Product URL and Back does not bounce);
  //   - several domain-qualified matches -> an explicit disambiguation;
  //   - none -> an honest not-found migration state;
  //   - a transport/schema failure -> a Product error state, never a fall back to the
  //     legacy screen.
  let { kind = 'service', name = '' } = $props();

  let phase = $state('resolving'); // resolving | ambiguous | notfound | error
  let matches = $state([]);
  let errorMsg = $state('');

  const listHref = $derived(kind === 'owner' ? fleetOwnersUrl() : fleetServicesUrl());
  const kindLabel = $derived(kind === 'owner' ? 'owner' : 'service');

  // Match on the exact display label (the legacy name) or an exact key, never a fuzzy
  // substring, so a legacy name is never canonicalized to the wrong entity.
  function isExact(e) {
    return e.label === name || e.key === name;
  }

  onMount(async () => {
    try {
      const res = await api.fleetEntities({ kinds: [kind], text: name, limit: 20 });
      const exact = (res.entities || []).filter(isExact);
      if (exact.length === 1) {
        replaceHash(hashForHref(exact[0].href));
        return;
      }
      if (exact.length > 1) { matches = exact; phase = 'ambiguous'; return; }
      phase = 'notfound';
    } catch (e) {
      if (e instanceof SchemaCompatibilityError) errorMsg = 'The dashboard and backend API versions differ; reload to update.';
      else if (e instanceof ApiError) errorMsg = `Couldn't resolve this link (HTTP ${e.status}). ${e.message}`;
      else errorMsg = 'The Pacto backend is unavailable. Check your connection and retry.';
      phase = 'error';
    }
  });
</script>

<section class="migrate" data-testid="legacy-migration">
  {#if phase === 'resolving'}
    <p class="mg-status" role="status">Taking you to the {kindLabel}…</p>
  {:else if phase === 'ambiguous'}
    <h1>Which {kindLabel}?</h1>
    <p class="mg-lead" data-testid="legacy-migration-ambiguous">Several {kindLabel}s are named <strong>{name}</strong>. Pick the one you meant — this link is from an older URL that did not distinguish them.</p>
    <ul class="mg-list">
      {#each matches as m (m.kind + '::' + m.key)}
        <li><EntityLink ref={m} /></li>
      {/each}
    </ul>
    <a class="mg-link" href={listHref}>Browse all {kindLabel}s &rarr;</a>
  {:else if phase === 'notfound'}
    <h1>Not found</h1>
    <p class="mg-lead" data-testid="legacy-migration-notfound">No {kindLabel} named <strong>{name}</strong> is in the current fleet. It may have been renamed or removed.</p>
    <a class="mg-link" href={listHref}>Browse all {kindLabel}s &rarr;</a>
  {:else}
    <h1>Couldn't open this link</h1>
    <div class="mg-error" role="alert" data-testid="legacy-migration-error">{errorMsg}</div>
    <a class="mg-link" href={listHref}>Browse all {kindLabel}s &rarr;</a>
  {/if}
</section>

<style>
  .migrate { display: flex; flex-direction: column; gap: var(--sp-3); max-width: 640px; }
  .mg-status { color: var(--c-text-3); }
  .mg-lead { color: var(--c-text-2); }
  .mg-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .mg-link { color: var(--c-accent); text-decoration: none; font-size: var(--text-sm); }
  .mg-link:hover { text-decoration: underline; }
  .mg-error { padding: var(--sp-3); border-radius: var(--radius-sm); background: var(--c-err-bg); border: 1px solid var(--c-err); color: var(--c-text); }
</style>
