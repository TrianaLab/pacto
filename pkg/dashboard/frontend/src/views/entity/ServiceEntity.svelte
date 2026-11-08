<script>
  import { fleetGraphFocusUrl, fleetEntityListUrl, fleetAttentionUrl, fleetServicesUrl, fleetEntityUrl } from '../../lib/router.ts';
  import { abbreviateDigests } from '../../lib/format.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import HelpTip from '../../components/HelpTip.svelte';
  import { businessDomainLabel, UNKNOWN_DOMAIN } from '../../lib/entityLabels.ts';
  import OwnershipFact from '../../components/OwnershipFact.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import ServiceDelivery from '../../components/ServiceDelivery.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import EvidenceList from '../../components/EvidenceList.svelte';
  import LimitationsSection from '../../components/LimitationsSection.svelte';
  import PostureBars from '../../components/viz/PostureBars.svelte';
  import DistributionBar from '../../components/viz/DistributionBar.svelte';
  import PolicyList from '../../components/PolicyList.svelte';
  import ConfigurationList from '../../components/ConfigurationList.svelte';
  import LifecycleBadge from '../../components/LifecycleBadge.svelte';
  import ProvisioningBadges from '../../components/ProvisioningBadges.svelte';
  import CopyableIdentifier from '../../components/CopyableIdentifier.svelte';
  import ServiceRevisionsFigure from '../../components/figure/ServiceRevisionsFigure.svelte';
  import { readinessSegments, signatureSegments, signatureState, segmentTotal, complianceSegments, READINESS_STATES } from '../../lib/distributions.ts';
  import { dropService, inService } from '../../lib/identityContext.ts';
  import { api } from '../../lib/api.ts';
  import { miraServiceLink } from '../../lib/delivery.ts';

  // The principal operational service page. It renders ONLY the
  // product entity-detail payload (never the snapshot): owner + ownership conflicts,
  // bounded revisions / deployments / expected-dependencies / dependents previews, the
  // observed/differences relationship summary, findings attributed to exact entities,
  // recent evidence, and limitations. Every related entity uses EntityLink; every
  // preview is truncation-honest via PreviewSection.
  //
  // The operational summary below is drawn from `summary`, the backend aggregate over
  // the service's COMPLETE target / revision / edge populations. It is deliberately not
  // computed from the previews beside it: those are capped at 200 items, so counting
  // them would quietly turn "the first 200 targets" into "this service".
  let { detail, deliveryState = 'unknown', refreshTick = 0, miraURL = '' } = $props();
  const d = $derived(detail.service ?? {});
  const key = $derived(detail.entity?.key ?? '');
  const bd = $derived(detail.entity?.businessDomain ?? '');
  // The graph focus is a meaningful continuation for the neighborhood previews
  // (dependencies/dependents/differences), whose exact contents live in the graph.
  const graphHref = $derived(fleetGraphFocusUrl('service', key));
  // A capped preview must have somewhere complete to send the user: the scoped
  // inventory lists page the SAME bounded Entities endpoint by canonical ServiceKey,
  // so "5 of 47" is a real answer away rather than a dead end.
  const allRevisionsHref = $derived(fleetEntityListUrl('revision', { service: key }));
  // "All revisions" is the exhaustive variant of a list that is usually already on the
  // page, so it opens on request -- unless nothing is running this service, in which
  // case it is the ONLY place its revisions appear and collapsing it leaves the page
  // saying nothing (the default has to be sensible, not uniform).
  const noneInUse = $derived((d.activeRevisions?.count ?? 0) === 0);
  // Neighbours Kubernetes permissions hide from this person: counted, never named.
  const hidden = (n, plus) => (n ? `${plus ? 'Plus ' : ''}${n === 1 ? '1 service' : `${n} services`} hidden by your Kubernetes permissions.` : '');
  const allTargetsHref = $derived(fleetEntityListUrl('target', { service: key }));
  // This page is about one service, so its own name is dropped from the targets,
  // revisions and findings listed on it: a long name repeated on every row hides the
  // part that differs. A target's name is its scope, kind and name; its environment is
  // a column of its own.
  const self = $derived(detail.entity?.label ?? '');
  // Open in Mira: Mira matches spans by service name only, so the link shows
  // only when no other service you can see shares this name.
  const miraHref = $derived(miraServiceLink(self, miraURL));
  let miraFor = $state('');
  $effect(() => {
    const name = self;
    miraFor = '';
    if (!miraHref || !name) return;
    let live = true;
    api.fleetEntities({ kinds: ['service'], text: name, limit: 50 }).then((r) => {
      if (live && !r.truncated && (r.entities ?? []).filter((e) => e.label === name).length === 1) miraFor = name;
    }, () => {});
    return () => { live = false; };
  });
  const own = (items) => (items ?? []).map((r) => inService(r, self));
  // The contract this service runs: the newest revision in use, else the newest known.
  const contract = $derived(
    d.activeRevisions?.items?.[0] ? { ref: d.activeRevisions.items[0], note: 'Latest in use' }
      : d.revisions?.items?.[0] ? { ref: d.revisions.items[0], note: 'Latest known' } : null,
  );
  const caps = $derived(d.capabilities ?? { items: [], total: 0, count: 0 });
  const unresolved = $derived(d.unresolvedDependencies?.items ?? []);
  const provides = $derived(detail.entity?.offering ?? '');
  const used = $derived(d.consumedOfferings ?? { items: [], total: 0, count: 0 });
  // Suggestions matched by name only, keyed by the unresolved ref they came from. They
  // never join the offerings this service declares that it uses.
  const inferred = $derived(new Map((d.inferredOfferings ?? []).map((s) => [s.ref, s])));
  const RANGE_TONE = { satisfied: 'ok', unsatisfied: 'warn', unknown: 'neutral' };
  const findings = $derived((d.findings?.items ?? []).map((it) => (it.entity ? { ...it, entity: inService(it.entity, self) } : it)));

  const sum = $derived(d.summary ?? {});
  const targets = $derived(sum.targets ?? 0);
  // How recently we looked: the one fact about "Recent evidence" the count cannot carry.
  const lastSeen = $derived(d.evidence?.items?.find((e) => e.at)?.at ?? '');
  // Drill-downs from the posture bars stay scoped to THIS service, by canonical key.
  // Sending a service page's "3 non-compliant" to the fleet-wide backlog would answer
  // a question the user did not ask and lose the three rows they clicked for.
  const attentionUrl = $derived((category) => fleetAttentionUrl({ service: key, category }));
  // Drift rows are only worth a line each when they are non-zero; a service with no
  // declared dependencies should not be told about seven kinds of zero. Each row links
  // to where its dependencies are listed: the attention item when one exists, else the
  // graph, where declared and observed edges are drawn side by side.
  const drift = $derived([
    { k: 'Declared dependencies', v: sum.declaredDependencies ?? 0, tone: '' },
    { k: 'Declared and observed', v: sum.reconciledMatched ?? 0, tone: 'ok' },
    { k: 'Declared, not observed', v: sum.declaredNotObserved ?? 0, tone: 'warn', href: graphHref },
    { k: 'Observed, not declared', v: sum.observedNotDeclared ?? 0, tone: 'warn', href: graphHref },
    // A golden path raises no attention item, so its rows link nowhere: its
    // Requirements section lists them.
    { k: 'Unresolved declarations', v: sum.unresolvedDeclared ?? 0, tone: 'warn', href: d.template ? undefined : attentionUrl('unresolved') },
    { k: 'Outside accepted range', v: sum.unsatisfiedRanges ?? 0, tone: 'warn', href: d.template ? undefined : attentionUrl('out-of-range') },
    { k: 'Cannot be observed', v: sum.insufficientEvidence ?? 0, tone: '', href: graphHref },
  ].filter((x) => x.v > 0));

  // Readiness is tallied over every known revision; each bucket opens those revisions.
  const readiness = $derived(readinessSegments(sum.readiness, Object.fromEntries(
    READINESS_STATES.map((st) => [st.field, fleetEntityListUrl('revision', { service: key, readiness: st.value })]),
  )));
  // Signatures are tallied over the revisions in use. When no source checked any of
  // them, a bar of one grey slice says less than the sentence that explains it.
  const signatures = $derived(signatureSegments(sum.signatures));
  const signed = $derived(segmentTotal(signatures));
  const noneChecked = $derived(signed > 0 && (sum.signatures?.notChecked ?? 0) === signed);
  const sigRows = $derived(d.signatures?.items ?? []);
  const relEmpty = $derived((d.limitations?.items ?? []).some((l) => l.limitation?.code === 'NEIGHBORHOOD_UNAVAILABLE')
    ? 'Relationships could not be computed. See the limitations below.'
    : 'No traffic observed for this service yet.');
  const current = $derived(d.currentRevision ? inService(d.currentRevision, self) : null);

  // A golden path (service.template, running nowhere) and a service created from one
  // (a declared service.origin). The creation checklist is derived by the fleet; only
  // a step with its signal reads as done, and Unknown keeps the Unknown tone.
  const gp = $derived(d.template ?? null);
  const origin = $derived(d.origin ?? null);
  const creation = $derived(d.creation ?? []);
  const gpSig = $derived(gp ? signatureState(gp.signature) : null);
  const VIA = { dependency: 'dependency', configuration_reference: 'configuration', policy_reference: 'policy' };
  const STEP_LABEL = { signature: 'Signature', contract: 'Contract', validation: 'Validation', plugin: 'Plugin', published: 'Published', fleet: 'Seen by Pacto', deployed: 'Deployed', provisioning: 'Provisioning' };
  const STEP_STATUS = {
    done: ['Done', 'ok'], planned: ['Planned', 'info'], skipped: ['Skipped', 'neutral'],
    unknown: ['Unknown', 'warn'], failed: ['Failed', 'err'], 'not-applicable': ['Not applicable', 'neutral'],
  };
  const stepStatus = (st) => STEP_STATUS[st] ?? ['Unknown', 'warn'];
  const hiddenCreated = $derived(gp?.hiddenCreatedServices ?? 0);
  const hiddenReqs = $derived(gp?.hiddenRequirements ?? 0);
</script>

<div class="svc-entity">
  <!-- The registry path stays in the page header's eyebrow. The business domain is a
       fact here because it is a place to go: it links to the domain page, and a
       service with none says so in a neutral tone. -->
  <section class="se-facts">
    <div class="se-fact" data-testid="service-domain">
      <span class="se-k">Domain</span>
      {#if bd}
        <EntityLink ref={{ kind: 'domain', key: bd, label: bd }} showStatus={false} showKind={false} />
      {:else if detail.entity?.businessDomainUnknown}
        <!-- Unknown is not No domain: an older operator, an unread contract or sources
             that disagree leave Pacto unable to tell, and it says so. -->
        <IdentityBadge label={businessDomainLabel(UNKNOWN_DOMAIN)} tone="neutral" title="Pacto cannot tell this service's domain: no readable contract revision names one, and its sources report none they agree on." describe />
      {:else}
        <IdentityBadge label={businessDomainLabel('')} tone="neutral" title="Its contract declares no service.domain." describe />
      {/if}
    </div>
    {#if d.ownership}
      <div class="se-fact" data-testid="service-owner">
        <span class="se-k">Owner</span>
        <!-- Both unowned states lead to this service's own `unowned` attention item,
             the population the fleet counts it in. -->
        <OwnershipFact ownership={d.ownership} unownedHref={fleetAttentionUrl({ service: key, category: 'unowned' })} declaredIn={d.ownerRevision ? inService(d.ownerRevision, self) : null} />
      </div>
    {/if}
    <div class="se-fact" data-testid="service-contract">
      <span class="se-k">Contract</span>
      {#if contract}
        <EntityLink ref={inService(contract.ref, self)} showStatus={false} showKind={false} />
        <span class="se-note">{contract.note}</span>
      {:else}
        <span class="se-none">No known revision</span>
      {/if}
    </div>
    {#if origin}
      <div class="se-fact" data-testid="service-origin">
        <span class="se-k">Created from</span>
        <IdentityBadge label="declared" tone="neutral" title="Its contract records this origin. Pacto did not observe the creation." describe />
        {#if origin.goldenPath}
          <EntityLink ref={origin.goldenPath} showStatus={false} showKind={false} />
          {#if origin.revision}<EntityLink ref={origin.revision} showStatus={false} showKind={false} />{/if}
        {:else}
          <code class="se-code">{origin.ref}</code>
          {#if origin.reason}<span class="se-note">{origin.reason}</span>{/if}
        {/if}
      </div>
    {/if}
    {#if provides}
      <div class="se-fact" data-testid="service-offering">
        <span class="se-k">Provides</span>
        <EntityLink ref={{ kind: 'offering', key: provides, label: provides }} showStatus={false} showKind={false} />
      </div>
    {/if}
    <div class="se-fact" data-testid="service-offerings-used">
      <span class="se-k">Platform offerings used</span>
      {#if (used.items ?? []).length}
        <ul class="se-chips">
          {#each used.items as u, ui (`${u.offering}:${u.provider?.key}:${u.via}:${ui}`)}
            <li class="se-use">
              <a class="se-chip" href={fleetEntityUrl('offering', u.offering)}>{u.offering}</a>
              <span class="se-note">from {u.provider?.label ?? u.provider?.key}</span>
              {#if u.rangeStatus}<IdentityBadge label={u.rangeStatus} tone={RANGE_TONE[u.rangeStatus] ?? 'neutral'} title={u.compatibility ? `Accepts ${u.compatibility}` : ''} describe />{/if}
              <LifecycleBadge lifecycle={u.providerLifecycle} detail={false} />
              <ProvisioningBadges statuses={u.provisioning ?? []} />
            </li>
          {/each}
          {#if used.truncated}<li class="se-note">+{(used.total ?? 0) - (used.count ?? 0)} more</li>{/if}
        </ul>
      {:else}
        <span class="se-none">None resolved</span>
        <span class="se-note">Unresolved references are not counted</span>
      {/if}
    </div>
    <div class="se-fact" data-testid="service-capabilities">
      <span class="se-k">Declared capabilities</span>
      <HelpTip label="Declared capabilities" text="Health, metrics and extension assertions this service's contract makes about itself." />
      {#if (caps.items ?? []).length}
        <ul class="se-chips">
          {#each caps.items as c (c)}<li><a class="se-chip" href={fleetServicesUrl({ capability: c })}>{c}</a></li>{/each}
          {#if caps.truncated}<li class="se-note">+{(caps.total ?? 0) - (caps.count ?? 0)} more</li>{/if}
        </ul>
      {:else}
        <span class="se-none">None declared</span>
      {/if}
    </div>
    <div class="se-fact" data-testid="service-sources">
      <span class="se-k">Seen by</span>
      {#if (d.sources?.items ?? []).length}
        <ul class="se-chips">
          {#each d.sources.items as src (src.key)}<li><EntityLink ref={src} showStatus={false} showKind={false} /></li>{/each}
          {#if d.sources.truncated}<li class="se-note">+{d.sources.total - d.sources.count} more</li>{/if}
        </ul>
      {:else}
        <span class="se-none">No source</span>
      {/if}
    </div>
  </section>

  {#if d.ownership?.conflicts?.count}
    <!-- Each conflict carries a canonical revision key, so joining them into one prose
         sentence buried the owner names -- the actual point -- inside 71 characters of
         hex apiece. One conflict per line, digests abbreviated, exact key in the
         tooltip: the same facts, read as a list of revisions instead of a paragraph. -->
    <div class="se-conflicts" role="status">
      <div class="se-conflicts-head">
        <strong>Ownership conflict.</strong>
        <span>These revisions declare an owner other than {d.ownership.owner || 'the service owner'}:</span>
      </div>
      <ul class="se-conflict-list">
        {#each d.ownership.conflicts.items as c}<li title={c}>{abbreviateDigests(dropService(c, self))}</li>{/each}
        {#if d.ownership.conflicts.truncated}<li class="se-conflict-more">+{d.ownership.conflicts.total - d.ownership.conflicts.count} more</li>{/if}
      </ul>
    </div>
  {/if}

  {#if gp}
    <section class="se-gp" id="sec-golden-path" data-toc="Golden path" aria-labelledby="se-gp-h" data-testid="golden-path">
      <div class="se-gp-head">
        <h2 id="se-gp-h" class="t-section-title">Golden path</h2>
        <IdentityBadge label="Golden path" tone="info" title="Its contract declares service.template and nothing runs it." describe />
      </div>
      {#if detail.entity?.summary}<p class="se-lead t-body-2">{detail.entity.summary}</p>{/if}

      <h3 class="t-subsection-title">Create a service from it</h3>
      <div class="se-create" data-testid="golden-path-create">
        {#if gp.instantiate}
          <div class="se-cmd">
            <span class="se-k">CLI</span>
            <CopyableIdentifier value={gp.instantiate.cli} label="CLI command" iconOnly />
          </div>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long digest-pinned command scrolls sideways; focus lets a keyboard reader scroll it) -->
          <pre class="se-snippet" tabindex="0"><code>{gp.instantiate.cli}</code></pre>
          <div class="se-cmd">
            <span class="se-k">MCP</span>
            <CopyableIdentifier value={gp.instantiate.mcp} label="MCP call" iconOnly />
          </div>
          <!-- svelte-ignore a11y_no_noninteractive_tabindex (a long digest-pinned command scrolls sideways; focus lets a keyboard reader scroll it) -->
          <pre class="se-snippet" tabindex="0"><code>{gp.instantiate.mcp}</code></pre>
          {#if gp.instantiate.ref?.includes('@sha256:')}
            <p class="se-note">The command names the exact bytes shown here by digest. Set a trust config with --trust or PACTO_DELIVERY_TRUST to check the signature at creation.</p>
          {:else}
            <p class="se-note">The command names a local folder: it copies the files the folder holds when it runs, which may differ from the ones shown here.</p>
          {/if}
        {:else}
          <p class="se-none">{gp.instantiateReason || 'No command is available.'}</p>
        {/if}
      </div>

      <div class="se-fact" data-testid="golden-path-signature">
        <span class="se-k">Signature</span>
        <IdentityBadge label={gpSig.label} tone={gpSig.value === 'verified' ? 'neutral' : gpSig.tone} title={gp.signature?.reason ?? ''} describe />
        {#if gpSig.value === 'verified' && gp.signature?.signer}<span class="se-sig-signer">Signer {gp.signature.signer}</span>{/if}
        {#if gp.signature && gp.signatureNote}<span class="se-note">{gp.signatureNote}</span>{/if}
      </div>

      <h3 class="t-subsection-title">Plugins</h3>
      {#if (gp.plugins ?? []).length}
        <div class="data-scroll">
          <table class="data-table" data-testid="golden-path-plugins">
            <caption class="visually-hidden">Generate plugins the golden path names</caption>
            <colgroup><col style="width: 22%" /><col /><col style="width: 34%" /></colgroup>
            <thead><tr><th scope="col">Plugin</th><th scope="col">Description</th><th scope="col">Options</th></tr></thead>
            <tbody>
              {#each gp.plugins as p (p.name)}
                <tr>
                  <th scope="row"><code>{p.name}</code></th>
                  <td data-label="Description">{p.description || ''}</td>
                  <td data-label="Options">{#each Object.entries(p.options ?? {}) as [k, v] (k)}<code class="se-opt">{k}: {v}</code>{/each}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        <p class="se-note">Plugins run only when the person creating the service names them with --run-plugins.</p>
      {:else}
        <p class="se-none">It names no plugins.</p>
      {/if}

      <h3 class="t-subsection-title">Requirements</h3>
      <div data-testid="golden-path-requirements">
        {#if (gp.requirements?.items ?? []).length || (gp.unresolvedRequirements?.items ?? []).length}
          <ul class="se-reqs">
            {#each gp.requirements?.items ?? [] as u, ui (`${u.offering}:${u.provider?.key}:${u.via}:${ui}`)}
              <li>
                {#if u.offering}<a class="se-chip" href={fleetEntityUrl('offering', u.offering)}>{u.offering}</a><span class="se-note">from</span>{/if}
                <EntityLink ref={u.provider} showStatus={false} showKind={false} />
                <IdentityBadge label={VIA[u.via] ?? u.via} tone="neutral" />
                {#if u.rangeStatus}<IdentityBadge label={u.rangeStatus} tone={RANGE_TONE[u.rangeStatus] ?? 'neutral'} title={u.compatibility ? `Accepts ${u.compatibility}` : ''} describe />{/if}
                <LifecycleBadge lifecycle={u.providerLifecycle} detail={false} />
              </li>
            {/each}
            {#each gp.unresolvedRequirements?.items ?? [] as u (u.ref)}
              <li><IdentityBadge label="unresolved" tone="warn" title={u.reason || ''} describe /><code class="se-code">{u.requestedRef || u.ref}</code></li>
            {/each}
          </ul>
          {#if gp.requirements?.truncated}<p class="se-note">+{(gp.requirements.total ?? 0) - (gp.requirements.count ?? 0)} more</p>{/if}
        {:else if !hiddenReqs}
          <p class="se-none">It consumes no offering.</p>
        {/if}
        {#if hiddenReqs}<p class="se-hidden t-body-2">{hiddenReqs === 1 ? '1 requirement' : `${hiddenReqs} requirements`} on services you may not see.</p>{/if}
      </div>
    </section>

    <div class="se-wrap" data-testid="golden-path-created">
      <PreviewSection title="Services created from it" total={gp.createdServices?.total ?? 0} count={gp.createdServices?.count ?? 0} truncated={gp.createdServices?.truncated} viewAllHref={fleetServicesUrl({ createdFrom: key })} viewAllLabel="View all" empty="No service you can see declares it as its origin." help="Services whose contract records this golden path as its origin, matched by content digest.">
        <EntityRefList items={gp.createdServices?.items ?? []} showStatus={false} />
      </PreviewSection>
      <!-- Outside the section so the hidden count shows when every created service is
           hidden; the section links to the full list itself only when it is truncated. -->
      {#if hiddenCreated || ((gp.createdServices?.count ?? 0) > 0 && !gp.createdServices?.truncated)}
        <p class="se-hidden t-body-2">
          {#if hiddenCreated}{hiddenCreated === 1 ? '1 service' : `${hiddenCreated} services`} you may not see.{/if}
          {#if (gp.createdServices?.count ?? 0) > 0 && !gp.createdServices?.truncated}<a href={fleetServicesUrl({ createdFrom: key })}>View all</a>{/if}
        </p>
      {/if}
    </div>
  {/if}

  {#if creation.length}
    <section class="se-gp" id="sec-creation" data-toc="Creation status" aria-labelledby="se-creation-h">
      <h2 id="se-creation-h" class="t-section-title">Creation status</h2>
      <p class="se-lead t-body-2">Derived from what Pacto observes now. Pacto keeps no record of the creation itself.</p>
      <ul class="se-steps" data-testid="creation-status">
        {#each creation as st, i (`${st.step}:${st.name ?? ''}:${i}`)}
          {@const [label, tone] = stepStatus(st.status)}
          <li data-step={st.step}>
            <span class="se-step">{STEP_LABEL[st.step] ?? st.step}{st.name ? ` ${st.name}` : ''}</span>
            <IdentityBadge {label} {tone} />
            {#if st.detail}<span class="se-note">{st.detail}</span>{/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}

  <!-- ── operational summary ─────────────────────────────────────────────────
       Three orthogonal questions a service owner opens this page to answer, in the
       order they are usually asked: does the running system obey the contract, do we
       know WHICH contract each instance is running, and how recently did we look.
       Keeping them as three bars rather than one health score is the point: a target
       can be compliant against a revision we only guessed at, and a fleet of "unknown"
       is not a fleet of failures. -->
  <section class="se-summary" id="sec-operational-summary" data-toc="Operational summary" aria-labelledby="se-summary-h">
    <h2 id="se-summary-h" class="t-section-title">Operational summary</h2>
    <p class="se-lead t-body-2">
      {targets} operational {targets === 1 ? 'target' : 'targets'} ·
      {#if targets > 0 && (sum.revisionsInUse ?? 0) === 0 && (sum.revisions ?? 0) > 0}{sum.revisions} known {sum.revisions === 1 ? 'revision' : 'revisions'}, none matched to a running target{:else}{sum.revisionsInUse ?? 0} of {sum.revisions ?? 0} known {(sum.revisions ?? 0) === 1 ? 'revision' : 'revisions'} in use{/if}{(sum.invalidRevisions ?? 0) > 0 ? ` · ${sum.invalidRevisions} invalid` : ''}
    </p>

    <PostureBars
      summary={sum}
      {attentionUrl}
      empty="Nothing running has been seen for this service yet."
    />

    {#if drift.length > 0}
      <h3 class="t-subsection-title">Dependency drift</h3>
      <ul class="se-drift">
        {#each drift as x (x.k)}
          <li class={x.tone ? `tone-${x.tone}` : ''}>{#if x.href}<a href={x.href}><b>{x.v}</b> {x.k}</a>{:else}<b>{x.v}</b> {x.k}{/if}</li>
        {/each}
      </ul>
    {/if}

    {#if segmentTotal(readiness) > 0 || signed > 0}
      <h3 class="t-subsection-title">Contract readiness and signatures</h3>
      {#if segmentTotal(readiness) > 0}
        <DistributionBar title="Readiness of known revisions" level={4} description="Readiness is declared and gated per revision." segments={readiness} total={sum.revisions ?? null} />
      {/if}
      {#if noneChecked}
        <p class="se-none t-body-2" data-testid="signatures-not-checked">No source that read these revisions checks signatures.</p>
      {:else if signed > 0}
        <DistributionBar title="Bundle signatures of revisions in use" level={4} description="Verified means signed by the identity the revision's own contract declares in publish.sign." segments={signatures} total={sum.revisionsInUse ?? null} />
      {/if}
    {/if}
  </section>

  <!-- Findings are confirmed problems attributed to exact entities. They used to sit
       below five inventory previews, so on a service with revisions the first thing a
       reader saw was a list of names and the last was the reason they were called.
       Requirement 13: active failures are never collapsed and never below the
       inventory. -->
  {#if (d.findings?.count ?? 0) > 0}
    <PreviewSection title="Findings" tone="err" total={d.findings?.total ?? 0} count={d.findings?.count ?? 0} truncated={d.findings?.truncated}>
      <FindingList items={findings} />
    </PreviewSection>
  {/if}

  <div class="se-wrap" data-testid="service-relationships">
    <PreviewSection title="Observed traffic and differences" total={d.relationships?.total ?? null} count={d.relationships?.count ?? 0} truncated={d.relationships?.truncated} viewAllHref={graphHref} viewAllLabel="Open differences view" empty={relEmpty} help="What Pacto has actually seen this service talk to, and where that disagrees with what its contract declares.">
      <RelationshipList items={d.relationships?.items ?? []} selfKey={key} showClaims />
    </PreviewSection>
    {#if miraHref && miraFor === self}
      <p class="t-body-2 se-mira">
        <a href={miraHref} target="_blank" rel="noopener noreferrer" data-testid="service-mira-link">Open in Mira</a>
        <HelpTip label="Open in Mira" text="Opens this service's spans in Mira. Mira matches by service name only." />
      </p>
    {/if}
  </div>

  <!-- Inventory: WHAT this service is made of. It answers a navigation question, not an
       operational one, so it comes after the summary and the findings.
       The two lists a reader almost always wants -- what is running, and which revisions
       it is running -- stay open; the exhaustive variants of the same lists open on
       request. -->
  <!-- Targets get the full width: a target's name is its scope, kind and name, and its
       environment is a column of its own beside it. -->
  <div class="se-wrap" data-testid="service-targets">
    <PreviewSection title="Operational targets" total={d.deployments?.total ?? 0} count={d.deployments?.count ?? 0} truncated={d.deployments?.truncated} viewAllHref={allTargetsHref} viewAllLabel="View all targets" empty="No running target observed.">
      <EntityRefList items={own(d.deployments?.items)} showKind={false} env />
    </PreviewSection>
  </div>

  {#if (d.revisions?.total ?? 0) > 0}
    <ServiceRevisionsFigure revisions={d.revisions} current={d.currentRevision ?? null} {self} allHref={allRevisionsHref} />
  {/if}

  <div class="se-wrap" data-testid="service-delivery">
    <ServiceDelivery service={self} domain={detail.entity?.domain ?? ''} state={deliveryState} {refreshTick} />
  </div>

  <div class="se-grid se-grid--two">
    <PreviewSection title="Revisions in use" total={d.activeRevisions?.total ?? 0} count={d.activeRevisions?.count ?? 0} truncated={d.activeRevisions?.truncated} viewAllHref={allRevisionsHref} viewAllLabel="View all revisions" empty="No running target matches a revision yet." help="The revisions at least one running target is matched to. Newest first.">
      <EntityRefList items={own(d.activeRevisions?.items)} showStatus={false} />
    </PreviewSection>

    <!-- A configuration or policy reference is NOT a dependency: it does not enter the
         dependency graph, so nothing above and nothing in the graph would ever mention
         it, and the referenced service had no way to learn who reaches into it. This is
         the reverse side of the reference the revision page links forward. -->
    <PreviewSection title="Referenced by" collapsible={(d.referencedBy?.count ?? 0) > 0} open={false} total={d.referencedBy?.total ?? 0} count={d.referencedBy?.count ?? 0} truncated={d.referencedBy?.truncated} empty="No service references this service's configuration or policy." help="These services reference this one's configuration or policy contract. The graph shows runtime dependencies only, so these references stay off it.">
      <EntityRefList items={d.referencedBy?.items ?? []} showStatus={false} />
    </PreviewSection>
  </div>

  <!-- Full width, like targets: a revision's name is the service's name and its version,
       and a third of the page cut a long service name into four lines. -->
  <div class="se-wrap" data-testid="service-all-revisions">
  <PreviewSection title="All revisions" collapsible open={noneInUse} summary="Every revision Pacto knows, in use or not" total={d.revisions?.total ?? 0} count={d.revisions?.count ?? 0} truncated={d.revisions?.truncated} viewAllHref={allRevisionsHref} viewAllLabel="View all revisions" empty="No known revisions."
    help="Newest first, whether or not anything is running them. Readiness is declared per revision. Open one to see its gate.">
    <!-- Readiness is declared and gated per revision; the bar above tallies those
         revisions, each with its own gate. -->
    <EntityRefList items={own(d.revisions?.items)} showStatus={false} showDate />
  </PreviewSection>
  </div>

  <div class="se-grid se-grid--two">
    <!-- The total is every declared dependency: the ones a fleet service provides, the
         external and unresolved ones listed under them, and those hidden by permissions. -->
    <div class="se-wrap" data-testid="service-dependencies">
      <PreviewSection title="Expected dependencies" help="Declared in the contract. External ones are delivered outside Pacto; unresolved ones match nothing Pacto knows." total={sum.declaredDependencies ?? 0} count={(d.dependencies?.count ?? 0) + unresolved.length} truncated={d.dependencies?.truncated || d.unresolvedDependencies?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty={hidden(d.hiddenDependencies, false) || 'No declared dependencies.'}>
        <EntityRefList items={d.dependencies?.items ?? []} showStatus={false} />
        {#if unresolved.length}
          <ul class="se-unresolved">
            {#each unresolved as u (u.ref)}
              <li data-testid="unresolved-dependency">
                {#if u.external}
                  <IdentityBadge label="external" />{' '}<code>{u.requestedRef || u.ref}</code>{#if u.version}{' '}<span class="se-note">{u.version}</span>{/if}
                {:else}
                  <IdentityBadge label="unresolved" tone="warn" title={u.reason || ''} describe />{' '}<code>{u.requestedRef || u.ref}</code>
                {/if}
                {#if inferred.get(u.ref)}
                  {@const s = inferred.get(u.ref)}
                  {' '}<IdentityBadge label="inferred" tone="neutral" title="Matched by name only; the contract does not declare it" describe />
                  {' '}<a href={fleetEntityUrl('offering', s.offering?.key)} data-testid="inferred-offering">may be the {s.offering?.label ?? s.offering?.key} offering</a>
                {/if}
              </li>
            {/each}
          </ul>
        {/if}
        {#if d.hiddenDependencies}<p class="se-hidden t-body-2" data-testid="hidden-dependencies">{hidden(d.hiddenDependencies, true)}</p>{/if}
      </PreviewSection>
    </div>

    <PreviewSection title="Dependents" help="Services that declare this one." total={d.dependents?.total ?? 0} count={d.dependents?.count ?? 0} truncated={d.dependents?.truncated} viewAllHref={graphHref} viewAllLabel="Explore in graph" empty={hidden(d.hiddenDependents, false) || 'Nothing depends on this service.'}>
      {#if (d.dependents?.total ?? 0) > 1}
        <!-- B4: the dependents' compliance over every visible dependent, never the
             preview; hidden ones are counted in the line below and never here. -->
        <DistributionBar title="Compliance of dependents" level={3} segments={complianceSegments(d.dependentsStatus)} total={d.dependents.total} />
      {/if}
      <EntityRefList items={d.dependents?.items ?? []} showStatus={false} />
      {#if d.hiddenDependents}<p class="se-hidden t-body-2" data-testid="hidden-dependents">{hidden(d.hiddenDependents, true)}</p>{/if}
    </PreviewSection>
  </div>

  {#if sigRows.length}
    <!-- One row per revision in use: what its signature check found and who the signer
         is. A verified badge always carries the signer, which its own contract names. -->
    <div class="se-wrap" data-testid="service-signatures">
      <PreviewSection title="Signatures" total={d.signatures?.total ?? 0} count={d.signatures?.count ?? 0} truncated={d.signatures?.truncated} help="The bundle signature of each revision in use, as checked by the source that read it from a registry.">
        <ul class="se-sigs">
          {#each sigRows as row (row.revision?.key)}
            {@const st = signatureState(row.checked ? row.signature : null)}
            <li>
              <EntityLink ref={inService(row.revision, self)} showStatus={false} showKind={false} />
              <IdentityBadge label={st.label} tone={st.tone} title={row.signature?.reason ?? ''} describe />
              {#if st.value === 'verified' && row.signature?.signer}<span class="se-sig-signer">Signer {row.signature.signer}, declared by its own contract</span>{/if}
              {#if st.value === 'unverified' && row.signature?.expectedSigner}<span class="se-sig-signer">Expected signer {row.signature.expectedSigner}, declared by its own contract</span>{/if}
              {#if row.referrersUnavailable}<span class="se-note">Referrers unavailable</span>{:else if row.checked}<span class="se-note">{row.referrers} {row.referrers === 1 ? 'referrer' : 'referrers'}</span>{/if}
            </li>
          {/each}
        </ul>
      </PreviewSection>
    </div>
  {/if}

  <!-- What the current revision declares about policies and configuration, with the
       schema content read from its bundle. -->
  <div class="se-grid se-grid--two">
    <div class="se-wrap" data-testid="service-policies">
      <PreviewSection title="Policies" total={d.policies?.total ?? 0} count={d.policies?.count ?? 0} truncated={d.policies?.truncated} empty={current ? "This service's current revision declares no policies." : 'No known revision.'}>
        {#if current}<p class="se-from t-body-2">From <EntityLink ref={current} showStatus={false} showKind={false} /></p>{/if}
        <PolicyList items={d.policies?.items ?? []} />
      </PreviewSection>
    </div>
    <div class="se-wrap" data-testid="service-configuration">
      <PreviewSection title="Configuration" total={d.configurations?.total ?? 0} count={d.configurations?.count ?? 0} truncated={d.configurations?.truncated} empty={current ? "This service's current revision declares no configuration." : 'No known revision.'}>
        {#if current}<p class="se-from t-body-2">From <EntityLink ref={current} showStatus={false} showKind={false} /></p>{/if}
        <ConfigurationList items={d.configurations?.items ?? []} />
      </PreviewSection>
    </div>
  </div>

  <!-- This is an overview page, so its sections stay open: traffic, dependencies,
       dependents and evidence are read here, not opened one by one. Limitations stay
       folded: the knowledge banner above already states that the page is uncertain. -->
  {#if (d.evidence?.count ?? 0) > 0}
    <PreviewSection title="Recent evidence" summary={lastSeen ? `Last observed ${formatDate(lastSeen)}` : ''} total={d.evidence?.total ?? 0} count={d.evidence?.count ?? 0} truncated={d.evidence?.truncated}>
      <EvidenceList items={(d.evidence?.items ?? []).map((ev) => ({ ...ev, target: inService(ev.target ?? {}, self) }))} />
    </PreviewSection>
  {/if}

  <!-- Limitations stay collapsible but NOT hidden: anything that makes this page's
       answer uncertain is already stated by the knowledge banner above, which is never
       collapsible. What is here is the per-entity detail behind that warning. -->
  <LimitationsSection page={d.limitations} />
</div>

<style>
  .svc-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .se-hidden { margin: var(--sp-2) 0 0; color: var(--c-text-3); }
  .se-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  /* A long chip list wraps inside its fact rather than widening the page. */
  .se-fact { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; max-width: 100%; }
  .se-chips { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-1); min-width: 0; }
  .se-chip { display: inline-block; font-size: var(--text-xs); padding: 2px 8px; border-radius: var(--radius-xs); color: var(--c-text); background: var(--c-surface-inset); border: 1px solid var(--c-border); text-decoration: none; overflow-wrap: anywhere; }
  .se-chip:hover { color: var(--c-accent); border-color: var(--c-accent); }
  .se-note { color: var(--c-text-3); font-size: var(--text-xs); }
  .se-use { display: inline-flex; align-items: center; gap: var(--sp-1); flex-wrap: wrap; min-width: 0; }
  .se-none { color: var(--c-text-3); }
  .se-wrap { display: contents; }
  .se-unresolved { list-style: none; margin: var(--sp-1) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-unresolved li { padding: var(--sp-1) var(--sp-2); overflow-wrap: anywhere; }
  .se-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .se-conflicts {
    display: flex; flex-direction: column; gap: var(--sp-2);
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .se-conflicts-head { display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline; }
  .se-conflict-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-conflict-list li { overflow-wrap: anywhere; }
  .se-conflict-more { color: var(--c-text-3); }
  .se-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); align-items: start; }
  .se-grid--two { grid-template-columns: repeat(auto-fit, minmax(min(320px, 100%), 1fr)); }
  /* Same frame as a PreviewSection -- this block is a section like any other, it just
     renders bars instead of a list. Its padding matches so the page has one rhythm. */
  .se-summary { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .se-summary h2, .se-summary h3 { margin: 0; }
  .se-lead { margin: 0; }
  .se-drift { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .se-drift li { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); border-left: 3px solid var(--tone-c, var(--c-neutral)); padding: 2px 10px; border-radius: var(--radius-xs); }
  .se-drift li a { color: inherit; text-decoration: none; }
  .se-drift li a:hover { color: var(--c-accent); }
  .se-sigs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-sigs li { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; padding: var(--sp-1) var(--sp-2); min-width: 0; }
  .se-sig-signer { font-size: var(--text-sm); color: var(--c-text-2); overflow-wrap: anywhere; min-width: 0; }
  .se-from { margin: 0 0 var(--sp-2); color: var(--c-text-3); }
  .se-drift li b { color: var(--c-text); font-variant-numeric: tabular-nums; }
  .tone-ok { --tone-c: var(--c-ok); }
  .tone-warn { --tone-c: var(--c-warn); }
  .se-gp { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); min-width: 0; }
  .se-gp h2, .se-gp h3 { margin: 0; }
  .se-gp-head { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .se-create { display: flex; flex-direction: column; gap: var(--sp-1); min-width: 0; }
  .se-cmd { display: flex; align-items: center; gap: var(--sp-2); }
  /* A long command scrolls by itself and never widens the page. */
  .se-snippet { margin: 0; padding: var(--sp-2) var(--sp-3); max-width: 100%; overflow-x: auto; background: var(--c-surface-inset); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); }
  .se-code { overflow-wrap: anywhere; font-size: var(--text-sm); }
  .se-opt { display: inline-block; margin: 0 var(--sp-1) var(--sp-1) 0; overflow-wrap: anywhere; }
  .se-reqs, .se-steps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .se-reqs li, .se-steps li { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; min-width: 0; }
  .se-step { font-weight: 600; min-width: 9rem; }
</style>
