<script>
  import { formatCount, shortDigest } from '../../lib/format.ts';
  import { formatRelative } from '../../lib/dateFormat.ts';
  import { cap } from '../../lib/cap.svelte.ts';
  import ShowAll from '../../components/ShowAll.svelte';
  import { externalClassNoun, externalJoinWords, serviceKeyName } from '../../lib/entityLabels.ts';
  import { fleetEntityUrl } from '../../lib/router.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import ExternalProvenance from '../../components/ExternalProvenance.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import ProvenanceMark from '../../components/ProvenanceMark.svelte';
  import OwnerLink from '../../components/OwnerLink.svelte';
  import DeclarePanel from '../../components/DeclarePanel.svelte';

  // An external system's page: the services that declare, call or use it first, then how
  // Pacto knows it. The catalog that classified it (or how its consumers join it) is in
  // view, then where each source recognized it: every declaration, configuration source
  // (inferred) and trace source (observed). Pacto observes no health for it, so nothing
  // here is a verdict.
  // canDeclare: the host plans claims for this caller (capabilities.declare);
  // declareQuery: declare=1 opens the claim panel, signed is the intent a signing
  // login came back with.
  let { detail, canDeclare = false, declareQuery = {} } = $props();
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
  // The inferred contract under pacto-inferred (517-S3). Its revisions come
  // operator-signed first, so the first is the one shown, and the mark says only what its
  // own signature check established (fleet.SignatureWords). A restricted view drops the
  // surface and says so (surfaceHidden).
  const provider = $derived(rec.provider ?? null);
  const shown = $derived(provider?.revisions?.[0] ?? null);
  const byOperator = $derived(shown?.signature?.source === 'operator');
  const providerSignature = (sig) => {
    if (!sig) return 'Signature not checked, publisher unknown';
    if (sig.source === 'operator') return "Published and signed by this installation's operator from what Pacto sees";
    if (sig.status === 'unsigned') return 'Unsigned, publisher unknown';
    if (!sig.status || sig.status === 'unknown') return 'Signature could not be checked, publisher unknown';
    return `Signed${sig.signer ? ` by ${sig.signer}` : ''}, not by this installation's operator (${sig.status})`;
  };
  const markTitle = $derived(`${providerSignature(shown?.signature)}; a range is never checked against it`);
  // When the surface was computed and whether that run saw everything: a partial run
  // carries earlier values forward, so they never read as current.
  const surfaceMeta = $derived.by(() => {
    const s = shown?.surface;
    if (!s) return '';
    const asOf = s.asOf ? `Computed ${formatRelative(s.asOf)}` : 'Computed at an unknown time';
    return s.completeness?.state === 'partial'
      ? `${asOf}; partial${s.completeness.limitations?.length ? ` (${s.completeness.limitations.join(', ')})` : ''}: some sources did not answer, so values may be carried over from earlier runs`
      : `${asOf}; complete`;
  });
  const SURFACE = [['hosts', 'Hosts'], ['ports', 'Ports'], ['clients', 'Clients']];
  const caps = { hosts: cap(12), ports: cap(12), clients: cap(12) };
  const seenWords = (v) => `seen by ${(v.sources ?? []).join(', ')}${v.lastSeen ? `, last seen ${formatRelative(v.lastSeen)}` : ''}`;
  // What changed between the two revisions shown first (517-S4). A change no source
  // checked the signatures of says so: it never raises an attention item.
  const change = $derived(provider?.change ?? null);
  const MAX_CHANGE_VALUES = 10;
  const capped = (verb, xs) => (xs?.length ? `${verb} ${xs.slice(0, MAX_CHANGE_VALUES).join(', ')}${xs.length > MAX_CHANGE_VALUES ? ` and ${formatCount(xs.length - MAX_CHANGE_VALUES)} more` : ''}.` : '');
  const changeTip = $derived(change ? [
    change.openapiFrom || change.openapiTo ? `OpenAPI ${change.openapiFrom || 'none'} → ${change.openapiTo || 'none'}.` : '',
    capped('Surface added', change.added), capped('Surface removed', change.removed),
    change.checked ? "This installation's operator signed both revisions." : "No source checked these revisions' signatures, so this change raises no attention item.",
  ].filter(Boolean).join(' ') : '');
  // Who owns it (517-S5): the claiming service's verified owner, a claim the viewer
  // cannot see, or nobody yet. Routing to the default approvers grants no right, so
  // the Claim button follows canDeclare and the plan's refusal, never the routing.
  const claim = $derived(rec.claim ?? null);
  // More than one claim: none counts, and only retracting settles it, so no Claim button.
  const claimants = $derived(rec.claimants ?? 0);
  const claimable = $derived(canDeclare && rec.class !== 'other-egress' && !claim?.hidden && claimants < 2);
  let claimToggled = $state(null);
  const claimOpen = $derived(claimToggled ?? !!declareQuery.open);
  const ROUTING = "Routed to the installation's default approvers (routing only; it grants no right)";
  // The claim was established by one person at one version; the newest revision may be a later publish.
  const claimTip = $derived(claim ? `Claim established by ${claim.by || 'an unnamed person'}${claim.declaredVersion ? ` at ${claim.declaredVersion}` : ''}${claim.ref ? `; newest revision ${claim.ref}` : ''}` : '');
  // The owner as its canonical key names it: a DRI-only owner is dri:<name>, never team:<name>.
  const claimOwner = $derived(claim?.owner ? (claim.ownerKey?.startsWith('dri:') ? { dri: claim.owner } : { team: claim.owner }) : null);
  const claimer = $derived(claim?.service ? serviceKeyName(claim.service) : '');
  const consumerUse = (c) => (c.provenance === 'observed' ? 'observed only' : c.provenance === 'inferred' ? 'inferred only' : c.required ? 'required' : 'optional');
</script>

<div class="external-entity">
  {#if x.foldedInto}
    <p class="xe-note t-body-2" data-testid="external-folded-into">This answer folds it into <a href={fleetEntityUrl('external', x.foldedInto)}>Other egress</a>, beyond the cap on how many one answer names, so the list and the graph do not show it. The answer is partial.</p>
  {/if}
  {#if rec.class === 'other-egress'}
    <p class="xe-note t-body-2" data-testid="external-folded">This node folds {plural(rec.folded ?? 0, 'external system', 'external systems')}, the least consumed beyond the cap on how many one answer names. The answer is partial; each folded system keeps its own identity in the snapshot.</p>
  {/if}

  {#if rec.class !== 'other-egress'}
    <section class="xe-section" id="sec-owner" data-toc="Owner" aria-labelledby="xe-owner-h" data-testid="external-owner">
      <h2 id="xe-owner-h" class="t-section-title">Owner</h2>
      <div class="xe-owner">
        {#if claimants > 1}
          <p class="t-body-2" data-testid="external-owner-claimants">Claimed by {claimants} services, so no claim counts until all but one are retracted</p>
        {:else if claim?.hidden}
          <p class="t-body-2" data-testid="external-owner-hidden">Claimed by a service you cannot see</p>
        {:else if claim}
          <p class="t-body-2 xe-wrap" data-testid="external-owner-claimed">{#if claimOwner}<OwnerLink owner={claimOwner} />{:else}No verified owner{/if}, claimed by <a href={fleetEntityUrl('service', claim.service)} title={claimer}><bdi>{claimer}</bdi></a> <bdi>{claim.version}</bdi> <ProvenanceMark label="About this claim" text={claimTip} /></p>
        {:else}
          <p class="t-body-2" data-testid="external-owner-unclaimed">Unclaimed <ProvenanceMark label="Who it is routed to" text={ROUTING} /></p>
        {/if}
        {#if claimable}
          <button type="button" class="btn btn-sm xe-claim" aria-expanded={claimOpen} aria-controls="xe-claim-panel" onclick={() => (claimToggled = !claimOpen)} data-testid="external-claim">Claim</button>
        {/if}
      </div>
      {#if claimable && claimOpen}
        <!-- Keyed on the signing login's intent: a host that signs in itself (the WASM demo)
             comes back to this page without reloading it, and the panel plans again. -->
        <div id="xe-claim-panel">{#key declareQuery.signed}<DeclarePanel serviceKey={`external:${rec.key}`} open signed={declareQuery.signed ?? ''} claimedName={claim ? claimer : ''} />{/key}</div>
      {/if}
    </section>
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
        {#if change}
          <p class="t-meta xe-wrap" data-testid="external-provider-change"><span class="xe-change-words">Changed <bdi>{change.from}</bdi> → <bdi>{change.to}</bdi>{change.at ? ` (${formatRelative(change.at)})` : ''}{change.checked ? '' : ', unchecked'}</span> <ProvenanceMark label="About this change" text={changeTip} /></p>
        {/if}
        {#if shown?.surface}
          <details class="disclosure">
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{byOperator ? "What this installation's operator saw" : 'What the record lists'}</summary>
            <p class="t-meta" data-testid="inferred-surface-meta">{surfaceMeta}</p>
            {#each SURFACE as [k, label] (k)}
              {#if shown.surface[k]?.length}
                <h3 class="t-label">{label} ({formatCount(shown.surface[k].length)})</h3>
                <ul class="xe-surface">
                  {#each caps[k].slice(shown.surface[k]) as v (v.value)}
                    <li><bdi>{v.value}</bdi> <span class="t-meta xe-sources">{seenWords(v)}</span></li>
                  {/each}
                </ul>
                <ShowAll cap={caps[k]} total={shown.surface[k].length} noun={label.toLowerCase()} testid={`inferred-surface-${k}-show-all`} />
              {/if}
            {/each}
          </details>
        {:else if shown}
          <p class="t-meta" data-testid="inferred-surface-hidden">{provider.surfaceHidden ? 'Surface hidden: your view is restricted' : 'This revision records no surface'}</p>
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
  .xe-owner { display: flex; align-items: center; justify-content: space-between; gap: var(--sp-2); flex-wrap: wrap; min-inline-size: 0; }
  .xe-owner p { margin: 0; min-inline-size: 0; }
  @media (pointer: coarse) { .xe-claim { min-block-size: 44px; min-inline-size: 44px; } }
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
