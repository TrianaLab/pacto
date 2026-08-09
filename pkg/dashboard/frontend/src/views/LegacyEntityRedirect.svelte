<script>
  import { onMount } from 'svelte';
  import { api, ApiError, SchemaCompatibilityError } from '../lib/api.ts';
  import { replaceHash, hashForHref, fleetServicesUrl, fleetOwnersUrl } from '../lib/router.ts';
  import EntityLink from '../components/EntityLink.svelte';

  // Migrates a legacy name-bearing URL (#/services/:name, #/services/:name/versions/:version,
  // #/owners/:id) to its canonical Product entity on a Fleet-capable host (Part 1, reopen
  // section 8). It resolves the display NAME through the Product Entities API and NEVER
  // fabricates a canonical key:
  //   - exactly one service match, no version -> replace the URL with the canonical service
  //     route (a replace, not a push, so a reload stays put and Back does not bounce);
  //   - exactly one service match WITH a version -> resolve the canonical Product Revision
  //     for that version (see resolveVersion) rather than dropping the version;
  //   - several same-named services -> an explicit SERVICE disambiguation (before any
  //     version lookup);
  //   - none -> an honest not-found migration state;
  //   - a transport/schema failure -> a Product error state, never a fall back to the
  //     legacy screen.
  let { kind = 'service', name = '', version = '' } = $props();

  let phase = $state('resolving'); // resolving | ambiguous | notfound | error
  let matches = $state([]);
  let errorMsg = $state('');
  // scope names WHAT could not be resolved uniquely, so the disambiguation / not-found copy
  // is honest about whether it is the service name or the requested version.
  let scope = $state('service'); // 'service' | 'version'

  const listHref = $derived(kind === 'owner' ? fleetOwnersUrl() : fleetServicesUrl());
  const kindLabel = $derived(kind === 'owner' ? 'owner' : 'service');

  // Match on the exact display label (the legacy name) or an exact key, never a fuzzy
  // substring, so a legacy name is never canonicalized to the wrong entity.
  function isExact(e) {
    return e.label === name || e.key === name;
  }

  // resolveVersion canonicalizes a legacy version bookmark to a Product Revision. It reads
  // the resolved service's revisions (scoped to that canonical service) and matches the
  // requested version to a canonical RevisionKey via the revision ref's EXPLICIT version
  // field -- never fabricated, never parsed from a display label. Exactly one match
  // canonicalizes (replace); several legitimate matches disambiguate; none is an honest
  // version not-found.
  async function resolveVersion(serviceRef) {
    const detail = await api.fleetEntityDetail('service', serviceRef.key);
    const revs = (detail.service?.revisions?.items ?? []).filter((r) => r.version === version);
    if (revs.length === 1) { replaceHash(hashForHref(revs[0].href)); return; }
    scope = 'version';
    if (revs.length > 1) { matches = revs; phase = 'ambiguous'; return; }
    phase = 'notfound';
  }

  onMount(async () => {
    try {
      const res = await api.fleetEntities({ kinds: [kind], text: name, limit: 20 });
      const exact = (res.entities || []).filter(isExact);
      // An ambiguous SERVICE name is disambiguated first, before any version lookup.
      if (exact.length > 1) { matches = exact; scope = 'service'; phase = 'ambiguous'; return; }
      if (exact.length === 0) { scope = 'service'; phase = 'notfound'; return; }
      // Exactly one service resolved: a version bookmark canonicalizes to a revision; a bare
      // service bookmark canonicalizes to the service entity.
      if (kind === 'service' && version) { await resolveVersion(exact[0]); return; }
      replaceHash(hashForHref(exact[0].href));
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
    <p class="mg-status" role="status">Taking you to the {scope === 'version' ? 'revision' : kindLabel}…</p>
  {:else if phase === 'ambiguous' && scope === 'version'}
    <h1>Which revision?</h1>
    <p class="mg-lead" data-testid="legacy-migration-ambiguous">Several revisions of <strong>{name}</strong> are version <strong>{version}</strong>. Pick the one you meant — this link is from an older URL that did not distinguish them.</p>
    <ul class="mg-list">
      {#each matches as m (m.kind + '::' + m.key)}
        <li><EntityLink ref={m} /></li>
      {/each}
    </ul>
    <a class="mg-link" href={listHref}>Browse all {kindLabel}s &rarr;</a>
  {:else if phase === 'ambiguous'}
    <h1>Which {kindLabel}?</h1>
    <p class="mg-lead" data-testid="legacy-migration-ambiguous">Several {kindLabel}s are named <strong>{name}</strong>. Pick the one you meant — this link is from an older URL that did not distinguish them.</p>
    <ul class="mg-list">
      {#each matches as m (m.kind + '::' + m.key)}
        <li><EntityLink ref={m} /></li>
      {/each}
    </ul>
    <a class="mg-link" href={listHref}>Browse all {kindLabel}s &rarr;</a>
  {:else if phase === 'notfound' && scope === 'version'}
    <h1>Version not found</h1>
    <p class="mg-lead" data-testid="legacy-migration-notfound">No revision of <strong>{name}</strong> is version <strong>{version}</strong> in the current fleet. It may have been superseded or removed.</p>
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
