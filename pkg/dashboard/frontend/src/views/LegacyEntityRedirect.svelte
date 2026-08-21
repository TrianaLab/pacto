<script>
  import { onMount } from 'svelte';
  import { api, ApiError, SchemaCompatibilityError } from '../lib/api.ts';
  import { boundedMatches, NAME_LOOKUP_LIMIT } from '../lib/entityResolve.ts';
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
  //   - a lookup whose page did not carry every match -> an honest bounded state: that page
  //     proves neither uniqueness nor absence (see lib/entityResolve.ts);
  //   - a transport/schema failure -> a Product error state, never a fall back to the
  //     legacy screen.
  let { kind = 'service', name = '', version = '' } = $props();

  let phase = $state('resolving'); // resolving | ambiguous | notfound | bounded | error
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

  // resolveVersion canonicalizes a legacy version bookmark to a Product Revision. It pages
  // the canonical service's revisions through the entities API (EntityFilter.Service) --
  // NOT the service detail's revisions preview, which is bounded and would make "no
  // revision is that version" a claim about the first page rather than about the service.
  // The requested version is matched via the revision ref's EXPLICIT version field, never
  // fabricated and never parsed from a display label. Exactly one match canonicalizes
  // (replace); several legitimate matches disambiguate; none is an honest version
  // not-found -- and all three only when the page carried every revision.
  async function resolveVersion(serviceRef) {
    const page = await api.fleetEntities({ kinds: ['revision'], service: serviceRef.key, limit: NAME_LOOKUP_LIMIT });
    const { matches: revs, complete } = boundedMatches(page, (r) => r.version === version);
    scope = 'version';
    if (!complete) { matches = revs; phase = 'bounded'; return; }
    if (revs.length === 1) { replaceHash(hashForHref(revs[0].href)); return; }
    if (revs.length > 1) { matches = revs; phase = 'ambiguous'; return; }
    phase = 'notfound';
  }

  onMount(async () => {
    try {
      const res = await api.fleetEntities({ kinds: [kind], text: name, limit: NAME_LOOKUP_LIMIT });
      const { matches: exact, complete } = boundedMatches(res, isExact);
      scope = 'service';
      // A page that did not carry every match proves neither uniqueness nor absence, so it
      // may not canonicalize the URL and may not report the name missing.
      if (!complete) { matches = exact; phase = 'bounded'; return; }
      // An ambiguous SERVICE name is disambiguated first, before any version lookup.
      if (exact.length > 1) { matches = exact; phase = 'ambiguous'; return; }
      if (exact.length === 0) { phase = 'notfound'; return; }
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
  {:else if phase === 'bounded'}
    <h1>Couldn't resolve this link</h1>
    <p class="mg-lead" data-testid="legacy-migration-bounded">
      {#if scope === 'version'}More revisions of <strong>{name}</strong> exist than this lookup could read at once, so Pacto cannot say which one is version <strong>{version}</strong> — or whether that version is still here.
      {:else}More {kindLabel}s match <strong>{name}</strong> than this lookup could read at once, so Pacto cannot say which one this older link meant — or whether it is still here.{/if}
      {#if matches.length}It may be one of these:{:else}Browse or search the list instead.{/if}
    </p>
    {#if matches.length}
      <ul class="mg-list">
        {#each matches as m (m.kind + '::' + m.key)}
          <li><EntityLink ref={m} /></li>
        {/each}
      </ul>
    {/if}
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
