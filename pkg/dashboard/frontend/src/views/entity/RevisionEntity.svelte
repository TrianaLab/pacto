<script>
  import { retrievabilityLabel, retrievabilityTone } from '../../lib/entityLabels.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import OwnershipFact from '../../components/OwnershipFact.svelte';
  import CopyableIdentifier from '../../components/CopyableIdentifier.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import ContractReference from '../../components/ContractReference.svelte';
  import RevisionDocs from '../../components/RevisionDocs.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';
  import HorizontalBars from '../../components/viz/HorizontalBars.svelte';
  import { formatDate } from '../../lib/dateFormat.ts';
  import { fleetEntityListUrl } from '../../lib/router.ts';

  // The revision page: an IMMUTABLE version of the service contract, not a deployment,
  // and the CONTRACT INSPECTOR -- the one place a user reads what this exact revision
  // actually declares. It renders the declared interfaces with their API operations,
  // configuration scopes with their bounded values, policies, capabilities and their
  // bindings, workload and state, ownership, readiness (score AND the derived checks),
  // declared dependencies, skills and docs, validation, provenance, exact + inferred
  // targets, and previous/next revisions.
  //
  // It used to reduce the first four of those to four count chips ("3 Interfaces"),
  // which made the page a dead end: the count was the end of the interface experience.
  // Counts are now section headers over the content itself.
  //
  // Content retrievability is shown as its OWN dimension: the revision is immutable,
  // but its content may not be retrievable -- we never call non-retrievable content
  // "immutable" just because the revision is known. Most of what lives in the bundle
  // FILES rather than in the contract (the raw OpenAPI document, JSON Schema bodies,
  // skill bodies, the SBOM) is not retained by the snapshot, so this page shows the
  // declared PATH to it and never pretends to have read it. Documentation is the
  // exception: RevisionDocs reads one doc body on demand, keyed by this revision, so
  // the docs are readable here without the page carrying them.
  let { detail } = $props();
  const d = $derived(detail.revision ?? {});
  const id = $derived(d.identity ?? {});
  const r = $derived(d.readiness ?? null);
  const o = $derived(d.ownership ?? null);
  const prov = $derived(d.provenance ?? {});
  const state = $derived(d.state ?? null);
  // The parent ServiceKey comes from the backend ref, never parsed out of the revision
  // key: a RevisionKey is not a ServiceKey with a suffix.
  const historyHref = $derived(d.service?.key ? fleetEntityListUrl('revision', { service: d.service.key }) : '');

  // The SBOM summary. The backend already bucketed every package by license over the
  // COMPLETE inventory and folded the long tail into `otherLicensed`, so the rows are
  // read verbatim and the tail becomes one honestly-named row rather than disappearing.
  const sbom = $derived(d.sbom ?? null);
  const SBOM_FORMATS = { spdx: 'SPDX', cyclonedx: 'CycloneDX' };
  const sbomFormat = $derived(SBOM_FORMATS[sbom?.format] ?? (sbom?.format || 'Unrecognized'));
  const sbomDir = $derived(sbom?.format === 'cyclonedx' ? 'sbom/*.cdx.json' : 'sbom/*.spdx.json');
  const licenseRows = $derived([
    ...(sbom?.licenses ?? []).map((l) => ({ label: l.license, value: l.count, tone: l.license === 'unspecified' ? 'warn' : 'info' })),
    ...(sbom?.otherLicensed ? [{ label: 'Less common licenses', value: sbom.otherLicensed, tone: 'neutral' }] : []),
  ]);
</script>

<div class="rev-entity">
  <section class="re-facts">
    <div class="re-fact"><span class="re-k">Service</span><EntityLink ref={d.service} showStatus={false} showKind={false} /></div>
    {#if d.version}<div class="re-fact"><span class="re-k">Version</span><span>{d.version}</span></div>{/if}
    <!-- The wire field is `pactoVersion`; uppercased as a label it read as PACTOVERSION,
         a field name sitting in a row of English words. The API already documents it as
         "Pacto version". -->
    {#if d.pactoVersion}<div class="re-fact"><span class="re-k">Pacto version</span><span>{d.pactoVersion}</span></div>{/if}
    <div class="re-fact"><span class="re-k">Valid</span><span>{d.valid ? 'Yes' : 'No'}</span></div>
    <div class="re-fact"><span class="re-k">Content</span><IdentityBadge label={retrievabilityLabel(id.identityClass, id.retrievable)} tone={retrievabilityTone(id.identityClass, id.retrievable)} /></div>
    {#if o}
      <div class="re-fact">
        <span class="re-k">Owner</span>
        <OwnershipFact ownership={o} />
      </div>
    {/if}
  </section>

  {#if id.digest || id.resolvedRef || id.requestedRef}
    <!-- The immutable content identity, in full and still copyable, one click away
         (requirement 13). Three rows of 71-character hex were the second thing on the
         page and the first thing every reader scrolled past; the badge in the facts
         strip above already says whether that content is retrievable, which is the part
         that changes what you do next. -->
    <details class="re-identity disclosure" data-testid="revision-identity">
      <summary><span class="disclosure-caret" aria-hidden="true">&#9656;</span>Content identity</summary>
      <div class="re-idrows">
        {#if id.digest}<div class="re-idrow"><span class="re-k">Digest</span><CopyableIdentifier value={id.digest} /></div>{/if}
        {#if id.resolvedRef}<div class="re-idrow"><span class="re-k">Resolved ref</span><CopyableIdentifier value={id.resolvedRef} /></div>{/if}
        {#if id.requestedRef && id.requestedRef !== id.resolvedRef}<div class="re-idrow"><span class="re-k">Requested ref</span><CopyableIdentifier value={id.requestedRef} /></div>{/if}
      </div>
    </details>
  {/if}

  {#if o?.contacts?.count}
    <!-- The declared contact block, on the entity that declared it. The facts strip
         above answers WHO owns this (or says there is no identity to route to); this
         answers HOW to reach whoever that is, which for a contacts-only owner is the
         entire declaration and was previously unreadable anywhere in the product.
         It stays a disclosure and stays text: a contact point is metadata, never an
         owner identity, so nothing here links to an owner page. -->
    <details class="re-contacts disclosure">
      <summary>
        <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
        Declared contacts ({o.contacts.total})
      </summary>
      <ul class="re-contactrows">
        {#each o.contacts.items as c, i (`${c.type}:${c.value}:${c.purpose ?? ''}:${i}`)}
          <li class="re-contactrow">
            <span class="re-k">{c.type || 'contact'}</span>
            <CopyableIdentifier value={c.value} />
            {#if c.purpose}<span class="re-cpurpose">{c.purpose}</span>{/if}
          </li>
        {/each}
      </ul>
      {#if o.contacts.truncated}
        <p class="re-ctrunc t-body-2">Showing {o.contacts.count} of {o.contacts.total} declared contact points.</p>
      {/if}
    </details>
  {/if}

  {#if r}
    <section class="re-readiness" id="sec-readiness" data-toc="Readiness">
      <div class="rr-head">
        <h2 class="t-section-title">Readiness</h2>
        <IdentityBadge label={r.passing ? 'Passing' : 'Not passing'} tone={r.passing ? 'ok' : 'warn'} />
      </div>
      <!-- The distinction a first-time user cannot guess from the word "readiness":
           this is the contract's own self-assessment of how prepared this revision is,
           scored against a gate its authors declared. It says nothing about whether the
           running system obeys the contract -- that is compliance, shown on the targets. -->
      <p class="rr-lead t-body-2">What the authors declared about this revision's preparedness — not a measurement of the running system.</p>
      <p class="rr-line t-body">Score {r.score} / {r.minScore} required · {r.doneCount} done · {r.partialCount} partial · {r.notDoneCount} not done{r.deferredCount ? ` · ${r.deferredCount} deferred` : ''}{r.expired ? ' · expired' : ''}</p>
      {#if (r.checks?.count ?? 0) > 0}
        <!-- Requirement 13 draws the line here and not on the section: FAILING readiness
             detail is an active problem and stays open; a passing gate's check-by-check
             breakdown is inspection detail, so it opens on request. The score line above
             is visible either way, so the state is never hidden -- only its evidence. -->
        <PreviewSection title="Readiness checks" level={3} role="subsection" collapsible open={!r.passing} summary={r.passing ? 'All declared checks accounted for' : 'Open to see which checks are outstanding'} total={r.checks?.total ?? 0} count={r.checks?.count ?? 0} truncated={r.checks?.truncated}>
          <ul class="re-checks">
            {#each r.checks.items as c (c.id)}
              <li>
                <IdentityBadge label={c.status || 'unknown'} tone={c.status === 'done' ? 'ok' : (c.status === 'not-done' ? 'warn' : 'neutral')} />
                <span class="rc-id">{c.id}</span>
                {#if c.category}<span class="rc-cat">{c.category}</span>{/if}
                {#if c.description}<span class="rc-desc">{c.description}</span>{/if}
              </li>
            {/each}
          </ul>
        </PreviewSection>
      {/if}
    </section>
  {:else}
    <!-- Triage sends people here from a "Revision has no readiness assessment" item, and
         the page used to answer with silence -- the one fact they came for was the one
         thing missing. Absence is a state worth naming, and naming it is also where the
         distinction gets taught: nothing declared is not the same as declared and failing. -->
    <section class="re-readiness" id="sec-readiness" data-toc="Readiness">
      <div class="rr-head">
        <h2 class="t-section-title">Readiness</h2>
        <IdentityBadge label="Not declared" tone="neutral" />
      </div>
      <p class="rr-lead t-body-2">This revision declares no readiness gate, so there is nothing here to pass or fail — which is not the same as failing one.</p>
    </section>
  {/if}

  <!-- Validation findings are confirmed defects in the contract itself, so they rank
       with readiness rather than below four inventory sections (requirement 13). -->
  {#if (d.validation?.count ?? 0) > 0}
    <PreviewSection title="Validation findings" tone="err" total={d.validation?.total ?? 0} count={d.validation?.count ?? 0} truncated={d.validation?.truncated}>
      <FindingList items={d.validation?.items ?? []} />
    </PreviewSection>
  {/if}

  <!-- ── what this revision declares ────────────────────────────────────────── -->

  <PreviewSection
    title="Interfaces"
    total={d.interfaces?.total ?? 0}
    count={d.interfaces?.count ?? 0}
    truncated={d.interfaces?.truncated}
    empty="This revision declares no interfaces."
  >
    <ul class="re-ifaces">
      {#each d.interfaces?.items ?? [] as i (i.name)}
        <li class="ri">
          <div class="ri-head">
            <span class="ri-name">{i.name}</span>
            {#if i.type}<IdentityBadge label={i.type} tone="info" />{/if}
            {#if i.visibility}<IdentityBadge label={i.visibility} tone="neutral" />{/if}
          </div>
          {#if i.ref}<div class="ri-ref"><span class="re-k">Document</span><CopyableIdentifier value={i.ref} /></div>{/if}
          {#if (i.operations?.count ?? 0) > 0}
            <!-- The operations were derived from the referenced document at build time,
                 so this is the real API surface, not a restatement of the declaration.
                 A revision with four interfaces of thirty operations each is 120 rows of
                 monospace before the reader reaches Configuration, so the SIZE of the
                 surface stays on screen and the surface itself opens on request
                 (requirement 13). Nothing is removed: the same rows, one click away, and
                 the flat cross-interface tool list further down still carries them. -->
            <details class="ri-ops disclosure">
              <summary>
                <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
                {i.operations.count}{i.operations.truncated ? ` of ${i.operations.total}` : ''} {i.operations.total === 1 && !i.operations.truncated ? 'operation' : 'operations'}
              </summary>
              <ul class="re-tools">
                {#each i.operations.items as t (t.name + t.method + t.path)}
                  <li>
                    {#if t.mutating}<IdentityBadge label="mutating" tone="warn" />{/if}
                    <span class="rt-method">{t.method}</span>
                    <span class="rt-path">{t.path}</span>
                    <span class="rt-name">{t.name}</span>
                    {#if t.summary}<span class="rt-summary">{t.summary}</span>{/if}
                  </li>
                {/each}
              </ul>
              {#if i.operations.truncated}
                <p class="ri-note">Showing {i.operations.count} of {i.operations.total} operations.</p>
              {/if}
            </details>
          {:else if i.operationsKnown}
            <p class="ri-note">The document was read and declares no operations.</p>
          {:else}
            <!-- Absence of evidence is not evidence of absence: an unread document must
                 never render as "this interface has no operations". -->
            <p class="ri-note">Operations unknown — the referenced document was not available when this revision was indexed.</p>
          {/if}
        </li>
      {/each}
    </ul>
  </PreviewSection>

  <PreviewSection
    title="Configuration"
    total={d.configurations?.total ?? 0}
    count={d.configurations?.count ?? 0}
    truncated={d.configurations?.truncated}
    empty="This revision declares no configuration."
  >
    <ul class="re-cfgs">
      {#each d.configurations?.items ?? [] as c (c.name)}
        <li class="ri">
          <div class="ri-head">
            <span class="ri-name">{c.name || 'default'}</span>
            <IdentityBadge label={c.required ? 'required' : 'optional'} tone={c.required ? 'warn' : 'neutral'} />
          </div>
          {#if c.schema}<div class="ri-ref"><span class="re-k">Schema</span><CopyableIdentifier value={c.schema} /></div>{/if}
          {#if c.ref}<div class="ri-ref"><span class="re-k">Reference</span><ContractReference value={c.ref} resolution={c.resolution} /></div>{/if}
          {#if (c.values?.count ?? 0) > 0}
            <!-- Same reasoning as the operation lists: the scope, whether it is required
                 and the reference it resolves to are what a reader scans for; the value
                 table is what they open when this is the scope they came for. -->
            <details class="ri-ops disclosure">
              <summary>
                <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
                {c.values.count}{c.values.truncated && typeof c.values.total === 'number' ? ` of ${c.values.total}` : ''} {c.values.count === 1 && !c.values.truncated ? 'value' : 'values'}
              </summary>
              <table class="re-kv">
                <thead><tr><th scope="col">Key</th><th scope="col">Value</th></tr></thead>
                <tbody>
                  {#each c.values.items as v (v.key)}<tr><td>{v.key}</td><td>{v.value}</td></tr>{/each}
                </tbody>
              </table>
              {#if c.values.truncated}<p class="ri-note">Showing {c.values.count} values{typeof c.values.total === 'number' ? ` of ${c.values.total}` : '; total unknown'}.</p>{/if}
            </details>
          {/if}
        </li>
      {/each}
    </ul>
  </PreviewSection>

  <PreviewSection
    title="Policies"
    total={d.policies?.total ?? 0}
    count={d.policies?.count ?? 0}
    truncated={d.policies?.truncated}
    empty="This revision declares no policies."
  >
    <table class="re-table">
      <thead><tr><th scope="col">Name</th><th scope="col">Kind</th><th scope="col">Definition</th><th scope="col">Target</th></tr></thead>
      <tbody>
        {#each d.policies?.items ?? [] as p (p.name)}
          <tr>
            <td data-label="Name">{p.name}</td>
            <td data-label="Kind">{p.ref ? 'Remote' : 'Local'}</td>
            <td data-label="Definition" class="rt-path">
              {#if p.ref}<ContractReference value={p.ref} resolution={p.resolution} />{:else}{p.schema || '—'}{/if}
            </td>
            <td data-label="Target">{p.target || '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </PreviewSection>

  <PreviewSection
    title="Capabilities"
    collapsible
    open={false}
    summary="What this revision offers, and how it is bound"
    total={d.capabilities?.total ?? 0}
    count={d.capabilities?.count ?? 0}
    truncated={d.capabilities?.truncated}
    empty="This revision declares no capabilities."
  >
    <ul class="re-caps">
      {#each d.capabilities?.items ?? [] as c, i (c.type + (c.ref ?? '') + i)}
        <li>
          <span class="ri-name">{c.type || c.ref}</span>
          {#if c.binding}
            <span class="rt-method">{c.binding.type || 'binding'}</span>
            {#if c.binding.interface}<span class="rc-cat">via {c.binding.interface}</span>{/if}
            {#if c.binding.path}<span class="rt-path">{c.binding.path}</span>{/if}
          {:else}
            <span class="rc-cat">no binding declared</span>
          {/if}
        </li>
      {/each}
    </ul>
  </PreviewSection>

  {#if d.workload || state}
    <section class="re-facts">
      {#if d.workload}<div class="re-fact"><span class="re-k">Workload</span><span>{d.workload}</span></div>{/if}
      {#if state?.type}<div class="re-fact"><span class="re-k">State</span><span>{state.type}</span></div>{/if}
      {#if state?.persistenceScope}<div class="re-fact"><span class="re-k">Persistence scope</span><span>{state.persistenceScope}</span></div>{/if}
      {#if state?.persistenceDurability}<div class="re-fact"><span class="re-k">Durability</span><span>{state.persistenceDurability}</span></div>{/if}
      {#if state?.dataCriticality}<div class="re-fact"><span class="re-k">Data criticality</span><span>{state.dataCriticality}</span></div>{/if}
    </section>
  {/if}

  {#if (d.dependencies?.count ?? 0) > 0}
    <!-- showClaims: on the contract inspector a dependency is a DECLARATION, so the row
         carries what was declared (requested ref, required, compatibility, lockfile pin)
         rather than only the name of the other service. -->
    <PreviewSection title="Declared dependencies" total={d.dependencies?.total ?? null} count={d.dependencies?.count ?? 0} truncated={d.dependencies?.truncated}>
      <RelationshipList items={d.dependencies?.items ?? []} showClaims />
    </PreviewSection>
  {/if}

  {#if (d.tools?.count ?? 0) > 0}
    <!-- The same operations, flat and cross-interface: this is the agent-facing tool
         list (the names MCP exposes), and it is also the safety net that keeps an
         operation reachable if the interface list above is truncated. -->
    <PreviewSection title="Tools exposed to agents" collapsible open={false} summary="Every operation, flat and cross-interface" total={d.tools?.total ?? 0} count={d.tools?.count ?? 0} truncated={d.tools?.truncated}>
      <ul class="re-tools">
        {#each d.tools.items as t (t.name + t.method + t.path)}
          <li>
            {#if t.mutating}<IdentityBadge label="mutating" tone="warn" />{/if}
            <span class="rt-method">{t.method}</span>
            <span class="rt-path">{t.path}</span>
            <span class="rt-name">{t.name}</span>
            {#if t.summary}<span class="rt-summary">{t.summary}</span>{/if}
          </li>
        {/each}
      </ul>
    </PreviewSection>
  {/if}

  {#if (d.skills?.count ?? 0) > 0}
    <PreviewSection title="Skills" collapsible open={false} total={d.skills?.total ?? 0} count={d.skills?.count ?? 0} truncated={d.skills?.truncated}>
      <ul class="re-chips">{#each d.skills.items as s (s)}<li>{s}</li>{/each}</ul>
    </PreviewSection>
  {/if}

  {#if (d.docs?.count ?? 0) > 0}
    <RevisionDocs revisionKey={detail.entity?.key ?? ''} docs={d.docs} />
  {/if}

  {#if sbom}
    <!-- The software inventory. The packages themselves stay in the bundle -- a snapshot
         holds every revision of every service, and one SBOM can list thousands -- so the
         page reports the exact package count and the license mix, and says where the
         inventory itself lives instead of implying it has been read here. -->
    <details class="re-sbom disclosure" id="sec-software-inventory" data-toc="Software inventory" data-testid="revision-sbom">
      <summary>
        <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
        <h2 class="t-section-title">Software inventory</h2>
        <span class="re-sbom-gist t-meta">{sbom.packages} {sbom.packages === 1 ? 'package' : 'packages'} · {sbomFormat}</span>
      </summary>
      <div class="re-sbom-panel">
        <div class="re-facts">
          <div class="re-fact"><span class="re-k">Format</span><span>{sbomFormat}</span></div>
          <div class="re-fact"><span class="re-k">Packages</span><span>{sbom.packages}</span></div>
        </div>
        <HorizontalBars
          title="Licenses"
          level={3}
          description="Every package counted once, including those that declare no license."
          items={licenseRows}
          unit="packages"
          unitOne="package"
          emptyLabel="This inventory records no packages."
        />
        <p class="ri-note">The package list itself is not retained by the dashboard; read it from the bundle's {sbomDir} directory.</p>
      </div>
    </details>
  {/if}

  {#if (d.metadata?.count ?? 0) > 0}
    <!-- Free-form contract metadata. It is author-controlled, so it is flattened and
         bounded at build time and shown verbatim: the dashboard assigns no meaning to
         any key. -->
    <PreviewSection
      title="Contract metadata"
      collapsible
      open={false}
      summary="Author-defined keys, shown verbatim"
      total={d.metadata?.total ?? null}
      count={d.metadata?.count ?? 0}
      truncated={d.metadata?.truncated}
    >
      <table class="re-kv">
        <thead><tr><th scope="col">Key</th><th scope="col">Value</th></tr></thead>
        <tbody>
          {#each d.metadata.items as m (m.key)}<tr><td>{m.key}</td><td>{m.value}</td></tr>{/each}
        </tbody>
      </table>
    </PreviewSection>
  {/if}

  <div class="re-grid">
    {#if (d.exactTargets?.count ?? 0) > 0}
      <PreviewSection title="Running here (exact match)" total={d.exactTargets?.total ?? 0} count={d.exactTargets?.count ?? 0} truncated={d.exactTargets?.truncated}>
        <EntityRefList items={d.exactTargets?.items ?? []} />
      </PreviewSection>
    {/if}
    {#if (d.inferredTargets?.count ?? 0) > 0}
      <PreviewSection title="Running here (inferred match)" total={d.inferredTargets?.total ?? 0} count={d.inferredTargets?.count ?? 0} truncated={d.inferredTargets?.truncated}>
        <EntityRefList items={d.inferredTargets?.items ?? []} />
      </PreviewSection>
    {/if}
  </div>

  <!-- Revision chronology. Previous/next are the engine's canonical adjacency (not a
       digest sort), and the third link is the complete, paged history of the parent
       service -- the version-history comprehension a two-item adjacency cannot give. -->
  {#if d.previous || d.next || historyHref}
    <section class="re-adjacent">
      {#if d.previous}<div class="re-adj"><span class="re-k">Previous revision</span><EntityLink ref={d.previous} showStatus={false} showKind={false} /></div>{/if}
      {#if d.next}<div class="re-adj"><span class="re-k">Next revision</span><EntityLink ref={d.next} showStatus={false} showKind={false} /></div>{/if}
      {#if historyHref}<div class="re-adj"><span class="re-k">Revision history</span><a href={historyHref} data-testid="revision-history-link">All revisions of this service</a></div>{/if}
    </section>
  {/if}

  {#if prov.source || (prov.sources?.count ?? 0) > 0 || prov.fetchedAt}
    <!-- Where this content came from. The identity block above says WHAT this revision
         is; this says who told us, and when we last heard it. Both are needed to answer
         "is what I am reading still what the registry has?" -- and both are inspection
         detail rather than the first screen (requirement 13). -->
    <details class="re-prov disclosure">
      <summary>
        <span class="disclosure-caret" aria-hidden="true">&#9656;</span>
        Provenance
        {#if prov.fetchedAt}<span class="t-meta">Fetched {formatDate(prov.fetchedAt)}</span>{/if}
      </summary>
      <div class="re-facts">
        {#if prov.source}<div class="re-fact"><span class="re-k">Primary source</span><span>{prov.source}</span></div>{/if}
        {#if (prov.sources?.count ?? 0) > 0}
          <div class="re-fact">
            <span class="re-k">Seen by</span>
            <span>{prov.sources.items.join(', ')}{prov.sources.truncated ? ` (${prov.sources.count} of ${prov.sources.total})` : ''}</span>
          </div>
        {/if}
        {#if prov.fetchedAt}<div class="re-fact"><span class="re-k">Fetched at</span><span>{formatDate(prov.fetchedAt)}</span></div>{/if}
      </div>
    </details>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" tone="warn" collapsible open={false} summary="What Pacto could not determine" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .rev-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .re-facts, .re-idrows, .re-adjacent { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .re-fact, .re-idrow, .re-adj { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .re-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .re-contactrows { list-style: none; margin: var(--sp-2) 0 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .re-contactrow { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .re-cpurpose { font-size: var(--text-sm); color: var(--c-text-3); }
  .re-ctrunc { margin: var(--sp-2) 0 0; color: var(--c-text-3); }
  .re-readiness, .re-sbom { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-4); background: var(--c-surface); }
  .re-readiness { display: flex; flex-direction: column; gap: var(--sp-3); }
  .rr-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .rr-head h2 { margin: 0; }
  .rr-line, .rr-lead { margin: 0; }
  /* A section that happens to collapse must not rank below one that does not, so the
     title keeps full section weight and colour inside the summary. */
  .re-sbom > summary h2 { margin: 0; color: var(--c-text); }
  .re-sbom > summary:hover h2 { color: var(--c-accent); }
  .re-sbom-gist { margin-left: auto; }
  .re-sbom-panel { display: flex; flex-direction: column; gap: var(--sp-3); margin-top: var(--sp-3); }
  /* Nested operation / value disclosures sit inside a card, so their summary is the
     quiet shared grey and only the caret marks them as openable. */
  .ri-ops > summary { color: var(--c-text-3); }
  .re-checks, .re-tools { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .re-checks li, .re-tools li { display: flex; gap: var(--sp-2); align-items: baseline; flex-wrap: wrap; font-size: var(--text-sm); }
  .rc-id, .rt-name { color: var(--c-text); }
  .rc-cat, .rc-desc, .rt-summary { color: var(--c-text-3); }
  .rt-method { font-family: var(--font-mono, monospace); text-transform: uppercase; color: var(--c-text-2); }
  .rt-path { font-family: var(--font-mono, monospace); color: var(--c-text); overflow-wrap: anywhere; }
  .re-chips { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .re-chips li { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); padding: 2px 10px; border-radius: var(--radius-xs); }
  .re-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); }

  /* Declared content. Each interface / configuration scope / capability is a card so
     its nested operations or values stay visibly attached to it rather than running
     together into one undifferentiated list. */
  .re-ifaces, .re-cfgs, .re-caps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .ri { display: flex; flex-direction: column; gap: var(--sp-2); padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface-inset); }
  .re-caps li { display: flex; gap: var(--sp-2); align-items: baseline; flex-wrap: wrap; font-size: var(--text-sm); }
  .ri-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .ri-name { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .ri-ref { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .ri-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); }
  .re-kv, .re-table { width: 100%; border-collapse: collapse; font-size: var(--text-sm); }
  .re-kv th, .re-table th { text-align: left; font-weight: 600; color: var(--c-text-3); font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; padding: var(--sp-1) var(--sp-2) var(--sp-1) 0; }
  .re-kv td, .re-table td { padding: var(--sp-1) var(--sp-2) var(--sp-1) 0; color: var(--c-text-2); border-top: 1px solid var(--c-border); overflow-wrap: anywhere; vertical-align: top; }
  .re-kv td:first-child { font-family: var(--font-mono, monospace); color: var(--c-text); }
  /* 320px: a four-column policy table cannot fit, so it becomes a stacked list with
     each cell keeping its column name as a prefix. */
  @media (max-width: 30rem) {
    .re-table thead { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap; }
    .re-table tr { display: block; border-top: 1px solid var(--c-border); padding: var(--sp-2) 0; }
    .re-table td { display: block; border: 0; padding: 0; }
    .re-table td::before { content: attr(data-label) ' '; color: var(--c-text-3); font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; }
  }
</style>
