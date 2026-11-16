<script>
  import { formatCount, shortDigest } from '../../lib/format.ts';
  import { externalClassNoun, externalJoinWords } from '../../lib/entityLabels.ts';
  import { fleetEntityUrl } from '../../lib/router.ts';
  import EntityLink from '../../components/EntityLink.svelte';

  // An external system's page: the services that declare it first, then how Pacto knows
  // it. The catalog that classified it (or how its consumers join it) is in view; the
  // evidence, one declaration per consumer, is one disclosure away. Pacto observes no
  // health for it, so nothing here is a verdict.
  let { detail } = $props();
  const x = $derived(detail.external ?? {});
  const rec = $derived(x.external ?? {});
  const consumers = $derived(x.consumers ?? []);
  const sources = $derived(rec.sources ?? []);
  const catalog = $derived(x.catalog ?? null);
  const hidden = $derived(rec.hiddenConsumers ?? 0);
  const totalConsumers = $derived(rec.consumersTotal ?? consumers.length);
  const labelOf = $derived(new Map(consumers.map((c) => [c.service?.key, c.service?.label || c.service?.key])));
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
  const ORIGIN = { default: 'Default catalog', organization: 'Organization catalog' };
  const signatureWords = (c) => (c.signature === 'verified' ? `signature verified${c.signer ? ` (${c.signer})` : ''}` : `signature ${c.signature || 'unknown'}`);
  const sourceLine = (s) => `${labelOf.get(s.service) || s.service} pacto.yaml dependencies[${s.index}] (name: ${s.dependency}${s.version ? `, version: ${s.version}` : ''})`;
</script>

<div class="external-entity">
  {#if x.foldedInto}
    <p class="xe-note t-body-2" data-testid="external-folded-into">This answer folds it into <a href={fleetEntityUrl('external', x.foldedInto)}>Other egress</a>, beyond the cap on how many one answer names, so the list and the graph do not show it. The answer is partial.</p>
  {/if}
  {#if rec.class === 'other-egress'}
    <p class="xe-note t-body-2" data-testid="external-folded">This node folds {plural(rec.folded ?? 0, 'external system', 'external systems')}, the least consumed beyond the cap on how many one answer names. The answer is partial; each folded system keeps its own identity in the snapshot.</p>
  {/if}

  <section class="xe-section" id="sec-consumers" data-toc="Consumers" aria-labelledby="xe-consumers-h">
    <h2 id="xe-consumers-h" class="t-section-title">Consumers</h2>
    <p class="t-meta xe-caption">{plural(totalConsumers, 'service declares', 'services declare')} {rec.class === 'other-egress' ? 'the external systems this node folds' : `this ${externalClassNoun(rec.class)}`}{consumers.length < totalConsumers ? `; the first ${formatCount(consumers.length)} are listed` : ''}.{#if hidden}{' '}<span data-testid="external-hidden-consumers">{plural(hidden, 'more service', 'more services')} you cannot see {hidden === 1 ? 'declares' : 'declare'} it.</span>{/if}</p>
    {#if consumers.length}
      <div class="data-scroll xe-frame" role="region" aria-label="Consumers">
        <table class="data-table xe-table" data-testid="external-consumers">
          <caption class="visually-hidden">The services that declare this external system, whether they require it and the versions they name</caption>
          <colgroup><col /><col style:width="7rem" /><col style:width="14rem" /></colgroup>
          <thead><tr><th scope="col">Service</th><th scope="col">Required</th><th scope="col">Declared version</th></tr></thead>
          <tbody>
            {#each consumers as c (c.service?.key)}
              <tr>
                <th scope="row"><EntityLink ref={c.service} showStatus={false} showKind={false} /></th>
                <td data-label="Required">{c.required ? 'required' : 'optional'}</td>
                <td data-label="Declared version">{#if c.versions?.length}<span class="cell-clip" title={c.versions.join(', ')}><bdi>{c.versions.join(', ')}</bdi></span>{:else}<span class="t-meta">none named</span>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  <section class="xe-section" id="sec-provenance" data-toc="Provenance" aria-labelledby="xe-prov-h" data-testid="external-provenance">
    <h2 id="xe-prov-h" class="t-section-title">Provenance</h2>
    {#if catalog && rec.catalog}
      <p class="t-body-2 xe-catalog" data-testid="external-catalog">
        {ORIGIN[catalog.origin] ?? catalog.origin} <span class="xe-digest" title={catalog.digest}>sha256:{shortDigest(catalog.digest)}</span>, entry <bdi>{rec.catalog.entry}</bdi>:
        {catalog.copy === 'registry' ? (catalog.origin === 'default' ? 'embedded copy, matches the signed registry artifact' : 'read from the registry') : 'embedded copy'}, {signatureWords(catalog)}{#if catalog.signature !== 'verified' && catalog.reason}{' '}({catalog.reason}){/if}.
      </p>
    {:else if rec.class !== 'other-egress'}
      <p class="t-body-2" data-testid="external-name-only">{externalJoinWords(rec.joinedBy).sentence}</p>
    {/if}
    {#if sources.length}
      <details class="disclosure xe-more" data-testid="external-sources">
        <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>More details: {plural(rec.sourcesTotal ?? sources.length, 'declaration', 'declarations')}</summary>
        <ul class="xe-sources">
          {#each sources as s (`${s.service}@${s.index}@${s.target ?? ''}`)}
            <li><bdi>{sourceLine(s)}</bdi>, <span class="t-meta" title={`Provenance: ${s.provenance}; joined by ${s.match}`}>{s.provenance}</span></li>
          {/each}
        </ul>
      </details>
    {/if}
  </section>
</div>

<style>
  .external-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .xe-section { display: flex; flex-direction: column; gap: var(--sp-2); }
  .xe-section h2, .xe-caption, .xe-catalog, .xe-note { margin: 0; }
  .xe-catalog { overflow-wrap: anywhere; }
  .xe-digest { font-family: var(--font-mono); font-size: 0.95em; }
  .xe-sources { margin: var(--sp-2) 0 0; padding-inline-start: var(--sp-4); display: flex; flex-direction: column; gap: var(--sp-1); overflow-wrap: anywhere; }
  .xe-frame { container: xetable / inline-size; }
  @container xetable (max-width: 36rem) {
    .xe-table, .xe-table tbody, .xe-table tr, .xe-table td, .xe-table tbody th { display: block; width: 100%; }
    .xe-table colgroup { display: none; }
    .xe-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip-path: inset(50%); }
    .xe-table tr + tr { border-top: 1px solid var(--c-border); }
    .xe-table td, .xe-table tbody th { border: none; padding: var(--sp-1) var(--sp-3); }
    .xe-table td[data-label]::before { content: attr(data-label); display: block; color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); }
  }
</style>
