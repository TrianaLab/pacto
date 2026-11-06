<script>
  import { onDestroy } from 'svelte';
  import { api } from '../lib/api.ts';
  import { formatCount } from '../lib/format.ts';
  import { inService } from '../lib/identityContext.ts';
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import LifecycleBadge from './LifecycleBadge.svelte';

  // One offering provider's versions, newest first. A range typed into "Versions
  // satisfying" asks the server which versions satisfy it and which is the latest
  // compatible one; the answer replaces the list until the range is cleared.
  let { offering = '', provider = null, versions = null, self = '' } = $props();
  const RANGE_TONE = { satisfied: 'ok', unsatisfied: 'warn', unknown: 'neutral' };
  const DEBOUNCE_MS = 300;

  let range = $state('');
  let checked = $state(null);
  let error = $state('');
  let timer;
  let generation = 0;

  function onInput(e) {
    range = e.currentTarget.value.trim();
    clearTimeout(timer);
    const gen = ++generation;
    if (!range) { checked = null; error = ''; return; }
    timer = setTimeout(async () => {
      try {
        const res = await api.fleetVersions({ offering, provider: provider?.key, range });
        if (gen === generation) { checked = res.providers?.[0]?.versions ?? null; error = ''; }
      } catch (err) {
        if (gen === generation) { checked = null; error = err?.message || 'The range could not be checked.'; }
      }
    }, DEBOUNCE_MS);
  }
  onDestroy(() => clearTimeout(timer));

  const shown = $derived(checked ?? versions ?? { items: [], total: 0, count: 0 });
  const hidden = $derived((shown.items ?? []).some((v) => v.inUse == null));
</script>

<div class="ov">
  {#if (shown.items ?? []).length}
    <div class="ov-row">
      <span class="ov-k">Versions</span>
      <ul class="ov-chips">
        {#each shown.items as v (v.revision.key)}
          <li>
            <EntityLink ref={inService(v.revision, self)} showStatus={false} showKind={false} />
            {#if v.inUse}<IdentityBadge label="in use" tone="ok" />{/if}
            <LifecycleBadge lifecycle={v.lifecycle} detail={false} />
            {#if v.rangeStatus}<IdentityBadge label={v.rangeStatus} tone={RANGE_TONE[v.rangeStatus] ?? 'neutral'} />{/if}
          </li>
        {/each}
        {#if shown.truncated}<li class="ov-note">+{formatCount(shown.total - shown.count)} more</li>{/if}
      </ul>
      {#if hidden}<span class="ov-note" title="Your permissions hide where this provider runs.">in use: not shown</span>{/if}
    </div>
  {/if}
  <div class="ov-row">
    <label class="ov-range"><span class="ov-k">Versions satisfying</span>
      <input class="input" type="text" value={range} oninput={onInput} placeholder="^16.0.0" spellcheck="false" autocomplete="off" />
    </label>
    {#if error}
      <span class="ov-err" role="alert">{error}</span>
    {:else if checked}
      <span class="ov-k">Latest compatible</span>
      {#if checked.latestCompatible}
        <EntityLink ref={inService(checked.latestCompatible, self)} showStatus={false} showKind={false} />
      {:else}
        <span class="ov-note">No version satisfies {range}</span>
      {/if}
    {/if}
  </div>
  {#if shown.note}<p class="ov-note">{shown.note}</p>{/if}
</div>

<style>
  .ov { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; }
  .ov-row, .ov-range { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .ov-chips { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-1) var(--sp-3); min-width: 0; }
  .ov-chips li { display: flex; align-items: center; gap: var(--sp-1); min-width: 0; }
  .ov-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .ov-range input { width: 14ch; }
  .ov-note { margin: 0; color: var(--c-text-3); font-size: var(--text-sm); overflow-wrap: anywhere; }
  .ov-err { color: var(--c-err); font-size: var(--text-sm); overflow-wrap: anywhere; }
</style>
