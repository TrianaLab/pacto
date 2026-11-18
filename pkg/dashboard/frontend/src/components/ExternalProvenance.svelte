<script>
  import PreviewSection from './PreviewSection.svelte';
  import ShowAll from './ShowAll.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import { fleetEntityUrl } from '../lib/router.ts';
  import { formatCount, shortDigest } from '../lib/format.ts';
  import { formatRelative } from '../lib/dateFormat.ts';

  // Where an external system was recognized: one row per declaration, per
  // configuration source that names it (inferred) and per trace source that saw a call
  // (observed). One external collects every consumer's sources, so the rows are capped
  // at 20 with "N more", and the answer's totals count what it does not list. Inferred
  // rows are muted and can be hidden; how Pacto knows each row is a discreet mark whose
  // title says it in full. The essentials (consumer, where, host, confidence) come
  // first; the source, catalog entry and read time sit under More details.
  /** @type {{ data: import('../lib/api.ts').ExternalEntityDetail['external'] }} */
  let { data } = $props();

  const KIND_WORDS = {
    env: 'Env', configMap: 'ConfigMap key', secretKeyRef: 'Secret key name', secretRef: 'Secret name',
    externalNameService: 'ExternalName Service', selectorlessService: 'Selectorless Service', serviceEntry: 'ServiceEntry',
    helmValues: 'Helm values', compose: 'Compose', kustomize: 'Kustomize', sdk: 'SDK', terraform: 'Terraform', crossplane: 'Crossplane',
  };
  // A possible match says why it is only possible: an SDK in a manifest, a resource the
  // repository provisions (the reason sits in the mark), else a key name alone.
  const POSSIBLE = { sdk: 'Possible (SDK only)', terraform: 'Possible', crossplane: 'Possible' };
  const PROVISIONED = new Set(['terraform', 'crossplane']);
  // A repository source (s.revision set) comes from the revision's inferred record.
  const inferredMark = (s) => s.revision
    ? `Inferred from ${s.service}'s repository at revision ${s.revision}${PROVISIONED.has(s.kind) ? ' (provisioned in this repository)' : ''}; never declared, so it never clears a gate`
    : `Inferred from configuration: ${s.source} read it${s.observedAt ? ` ${formatRelative(s.observedAt)}` : ''}; never declared, so it never clears a gate`;
  const MARK = { declared: 'D', observed: 'O', inferred: 'I' };

  const declared = $derived((data?.external?.sources ?? []).filter((s) => s.provenance === 'declared').map((s) => ({
    provenance: 'declared', service: s.service, kind: 'Contract',
    where: `pacto.yaml dependencies[${s.index}]${s.dependency ? ` (name: ${s.dependency}${s.superseded ? ', not in the newest revision' : ''})` : ''}`,
    host: null, confidence: s.required ? 'Declared, required' : 'Declared',
    mark: `Declared in ${s.service}'s contract${s.revision ? ` (${s.revision})` : ''}, joined by ${s.match}`,
    details: [s.version ? `version ${s.version}` : '', s.revision ? `revision ${s.revision}` : ''].filter(Boolean),
  })));
  const inferred = $derived((data?.inferredSources ?? []).map((s) => ({
    provenance: 'inferred', service: s.service, kind: KIND_WORDS[s.kind] ?? s.kind, where: s.where || '',
    host: s.host || '', confidence: s.confidence === 'likely' ? 'Likely' : (POSSIBLE[s.kind] ?? 'Possible (key name only)'),
    mark: inferredMark(s),
    details: [`source ${s.source}`, s.entry ? `catalog entry ${s.entry}` : 'no catalog entry', s.catalog ? `catalog ${shortDigest(s.catalog, 12)}` : '',
      s.revision ? `revision ${s.revision}` : s.observedAt ? `read ${formatRelative(s.observedAt)}` : 'read time unknown'].filter(Boolean),
  })));
  const observed = $derived((data?.observedSources ?? []).map((s) => ({
    provenance: 'observed', service: s.service, kind: 'Traces', where: s.source, host: null,
    confidence: `Observed, ${formatCount(s.count)} ${s.count === 1 ? 'call' : 'calls'}`,
    mark: `Observed: ${s.source} saw ${s.service} call it${s.lastSeen ? `, last ${formatRelative(s.lastSeen)}` : ''}`,
    details: [s.firstSeen ? `first seen ${formatRelative(s.firstSeen)}` : '', s.lastSeen ? `last seen ${formatRelative(s.lastSeen)}` : ''].filter(Boolean),
  })));

  let showInferred = $state(true);
  let details = $state(false);
  const rows = $derived([...declared, ...observed, ...(showInferred ? inferred : [])]);
  // What the answer counts, listed or not: the totals cover sources it did not list.
  const total = $derived(declared.length + Math.max(data?.observedSourcesTotal ?? 0, observed.length)
    + (showInferred ? Math.max(data?.inferredSourcesTotal ?? 0, inferred.length) : 0));
  const list = cap(20);
  const shown = $derived(list.slice(rows));
  const more = $derived(total - shown.length);
  const hidden = $derived(data?.external?.hiddenConsumers ?? 0);
</script>

<PreviewSection title="Where it was recognized" count={shown.length} {total} clip={false}
  help="Each contract that declares it, each configuration source that names it (inferred) and each trace source that saw a call (observed). Hosts are normalized: credentials, paths and queries are never read."
  empty="No source names it.">
  {#if inferred.length || rows.length}
    <div class="xp-tools">
      {#if inferred.length && (declared.length || observed.length)}
        <label class="xp-filter"><input type="checkbox" bind:checked={showInferred} data-testid="external-show-inferred" /> Show inferred</label>
      {/if}
      <button type="button" class="btn btn-sm" aria-expanded={details} onclick={() => { details = !details; }} data-testid="external-more-details">{details ? 'Fewer details' : 'More details'}</button>
    </div>
  {/if}
  <ul class="xp-list" data-testid="external-provenance">
    {#each shown as r, i (i)}
      <li class="xp-row" class:xp-inferred={r.provenance === 'inferred'} data-testid="external-source" data-provenance={r.provenance}>
        <div class="xp-cell" data-cell="service">
          <span class="xp-k">Consumer</span>
          <span class="xp-line">
            <span class="xp-mark" title={r.mark} aria-hidden="true">{MARK[r.provenance]}</span>
            <span class="visually-hidden">{r.mark}</span>
            <a href={fleetEntityUrl('service', r.service)} class="xp-trunc"><bdi title={r.service}>{r.service}</bdi></a>
          </span>
        </div>
        <div class="xp-cell" data-cell="where">
          <span class="xp-k">Found in</span>
          <span class="xp-line"><span class="xp-kind">{r.kind}</span> <bdi class="xp-trunc" title={r.where}>{r.where || 'unknown'}</bdi></span>
        </div>
        <div class="xp-cell" data-cell="host">
          <span class="xp-k">Host</span>
          <!-- Only an inferred row carries a host; elsewhere it is unknown, never "none". -->
          {#if r.host}<bdi class="xp-trunc" title={r.host}>{r.host}</bdi>{:else}<span class="t-meta">{r.host === null ? 'not recorded' : 'none'}</span>{/if}
        </div>
        <div class="xp-cell" data-cell="confidence">
          <span class="xp-k">Confidence</span>
          <span>{r.confidence}</span>
        </div>
        {#if details && r.details.length}
          <p class="xp-details t-meta" data-cell="details">{r.details.join(' · ')}</p>
        {/if}
      </li>
    {/each}
  </ul>
  <ShowAll cap={list} total={rows.length} noun="sources" testid="external-sources-show-all" />
  {#if more > 0}<p class="t-meta" data-testid="external-sources-more">{formatCount(more)} more</p>{/if}
  {#if hidden > 0}<p class="t-meta" data-testid="external-hidden-consumers">{formatCount(hidden)} hidden {hidden === 1 ? 'consumer' : 'consumers'}: their sources are not listed</p>{/if}
</PreviewSection>

<style>
  .xp-tools { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2) var(--sp-3); margin-block-end: var(--sp-2); }
  .xp-filter { display: inline-flex; align-items: center; gap: var(--sp-1); min-block-size: 32px; font-size: var(--text-sm); }
  /* A labelled stacked block per row on a narrow container, four columns on a wide
     one (SPEC 24.2): the container decides, never the viewport. */
  .xp-list { container-type: inline-size; list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
  .xp-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; }
  @container (min-width: 48rem) {
    .xp-row { grid-template-columns: minmax(14ch, 1fr) minmax(20ch, 1.6fr) minmax(16ch, 1.2fr) minmax(12ch, 0.8fr); align-items: start; }
    .xp-details { grid-column: 1 / -1; }
  }
  .xp-inferred { color: var(--c-text-2); }
  .xp-cell { display: flex; flex-direction: column; gap: 2px; min-inline-size: 0; }
  .xp-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .xp-line { display: flex; align-items: baseline; gap: var(--sp-2); min-inline-size: 0; }
  .xp-trunc { min-inline-size: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .xp-trunc bdi { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }
  .xp-kind { flex: none; color: var(--c-text-3); font-size: var(--text-sm); }
  .xp-mark { flex: none; display: inline-grid; place-items: center; inline-size: 1.4em; block-size: 1.4em; border: 1px solid var(--c-border); border-radius: var(--radius-xs); font-size: var(--text-xs); color: var(--c-text-3); cursor: help; }
  .xp-inferred .xp-mark { border-style: dashed; }
  .xp-details { margin: 0; overflow-wrap: anywhere; }
</style>
