<script>
  import { formatCount, shortDigest } from '../../lib/format.ts';
  import { externalClassNoun, externalJoinWords } from '../../lib/entityLabels.ts';
  import { fleetEntityUrl } from '../../lib/router.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import ExternalProvenance from '../../components/ExternalProvenance.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';

  // An external system's page: the services that declare, call or use it first, then how
  // Pacto knows it. The catalog that classified it (or how its consumers join it) is in
  // view, then where each source recognized it: every declaration, configuration source
  // (inferred) and trace source (observed). Pacto observes no health for it, so nothing
  // here is a verdict.
  let { detail } = $props();
  const x = $derived(detail.external ?? {});
  const rec = $derived(x.external ?? {});
  const consumers = $derived(x.consumers ?? []);
  const catalog = $derived(x.catalog ?? null);
  const hidden = $derived(rec.hiddenConsumers ?? 0);
  const totalConsumers = $derived(rec.consumersTotal ?? consumers.length);
  const plural = (n, one, many) => `${formatCount(n)} ${n === 1 ? one : many}`;
  const ORIGIN = { default: 'Default catalog', organization: 'Organization catalog' };
  const signatureWords = (c) => (c.signature === 'verified' ? `signature verified${c.signer ? ` (${c.signer})` : ''}` : `signature ${c.signature || 'unknown'}`);
  // Required and optional describe a declaration; a consumer no contract declares says
  // how Pacto knows it instead.
  // The inferred contract the operator published for it (517-S3). Its revisions come
  // operator-signed first, so the first is the one shown, and the mark says what its own
  // signature proves. A view that hides any source or consumer drops the surface.
  const provider = $derived(rec.provider ?? null);
  const shown = $derived(provider?.revisions?.[0] ?? null);
  const markTitle = $derived(shown?.signature?.source === 'operator'
    ? "Published and signed by this installation's operator from what Pacto sees; a range is never checked against it"
    : 'Unsigned, publisher unknown; a range is never checked against it');
  const SURFACE = [['hosts', 'Hosts'], ['ports', 'Ports'], ['clients', 'Clients']];
  const consumerUse = (c) => (c.provenance === 'observed' ? 'observed only' : c.provenance === 'inferred' ? 'inferred only' : c.required ? 'required' : 'optional');
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
    <p class="t-meta xe-caption">{plural(totalConsumers, 'service declares, calls or uses', 'services declare, call or use')} {rec.class === 'other-egress' ? 'the external systems this node folds' : `this ${externalClassNoun(rec.class)}`}{consumers.length < totalConsumers ? `; the first ${formatCount(consumers.length)} are listed` : ''}.{#if hidden}{' '}<span data-testid="external-hidden-consumers">{plural(hidden, 'more service', 'more services')} you cannot see {hidden === 1 ? 'declares, calls or uses' : 'declare, call or use'} it.</span>{/if}</p>
    {#if consumers.length}
      <div class="data-scroll xe-frame" role="region" aria-label="Consumers">
        <table class="data-table xe-table" data-testid="external-consumers">
          <caption class="visually-hidden">The services that declare, call or use this external system, whether they require it (or how Pacto knows them) and the versions they name</caption>
          <colgroup><col /><col style:width="7rem" /><col style:width="14rem" /></colgroup>
          <thead><tr><th scope="col">Service</th><th scope="col">Required</th><th scope="col">Declared version</th></tr></thead>
          <tbody>
            {#each consumers as c (c.service?.key)}
              <tr>
                <th scope="row"><EntityLink ref={c.service} showStatus={false} showKind={false} /></th>
                <td data-label="Required" data-testid="external-consumer-use">{consumerUse(c)}</td>
                <td data-label="Declared version">{#if c.versions?.length}<span class="cell-clip" title={c.versions.join(', ')}><bdi>{c.versions.join(', ')}</bdi></span>{:else}<span class="t-meta">none named</span>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </section>

  {#if provider}
    <details class="xe-inferred disclosure" id="sec-inferred-contract" data-toc="Inferred contract" data-testid="external-inferred-contract">
      <summary>
        <span class="disclosure-caret" data-motion aria-hidden="true"></span>
        <h2 class="t-section-title">Inferred contract</h2>
        <span class="t-meta">Range checks: unverified</span>
      </summary>
      <div class="xe-inferred-panel">
        <p class="t-body-2 xe-wrap"><bdi>{provider.repository}</bdi>{#if shown}, version <bdi>{shown.version}</bdi>{/if} <IdentityBadge label="inferred" title={markTitle} describe /></p>
        {#if shown?.surface}
          <details class="disclosure">
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>What Pacto saw</summary>
            {#each SURFACE as [k, label] (k)}
              {#if shown.surface[k]?.length}
                <h3 class="t-label">{label}</h3>
                <ul class="xe-surface">
                  {#each shown.surface[k] as v (v.value)}
                    <li><bdi>{v.value}</bdi> <span class="t-meta xe-sources">seen by {(v.sources ?? []).join(', ')}</span></li>
                  {/each}
                </ul>
              {/if}
            {/each}
          </details>
        {:else if shown}
          <p class="t-meta" data-testid="inferred-surface-hidden">Surface hidden: some sources are outside your view</p>
        {/if}
      </div>
    </details>
  {/if}

  <section class="xe-section" id="sec-provenance" data-toc="Provenance" aria-labelledby="xe-prov-h">
    <h2 id="xe-prov-h" class="t-section-title">Provenance</h2>
    {#if catalog && rec.catalog}
      <p class="t-body-2 xe-catalog" data-testid="external-catalog">
        {ORIGIN[catalog.origin] ?? catalog.origin} <span class="xe-digest" title={catalog.digest}>sha256:{shortDigest(catalog.digest)}</span>, entry <bdi>{rec.catalog.entry}</bdi>:
        {catalog.copy === 'registry' ? (catalog.origin === 'default' ? 'embedded copy, matches the signed registry artifact' : 'read from the registry') : 'embedded copy'}, {signatureWords(catalog)}{#if catalog.signature !== 'verified' && catalog.reason}{' '}({catalog.reason}){/if}.
      </p>
    {:else if rec.class !== 'other-egress'}
      <p class="t-body-2" data-testid="external-name-only">{externalJoinWords(rec.joinedBy).sentence}</p>
    {/if}
  </section>

  <ExternalProvenance data={x} />
</div>

<style>
  .external-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .xe-section { display: flex; flex-direction: column; gap: var(--sp-2); }
  .xe-section h2, .xe-caption, .xe-catalog, .xe-note { margin: 0; }
  .xe-catalog, .xe-wrap, .xe-surface li { overflow-wrap: anywhere; }
  .xe-inferred { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  .xe-inferred > summary h2 { margin: 0; color: var(--c-text); }
  .xe-inferred-panel { display: flex; flex-direction: column; gap: var(--sp-2); margin-top: var(--sp-3); }
  .xe-inferred-panel p, .xe-inferred-panel h3 { margin: 0; }
  .xe-surface { margin: 0 0 var(--sp-2); padding-left: var(--sp-4); }
  .xe-digest { font-family: var(--font-mono); font-size: 0.95em; }
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
