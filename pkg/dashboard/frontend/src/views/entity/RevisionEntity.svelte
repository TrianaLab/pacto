<script>
  import { retrievabilityLabel, retrievabilityTone } from '../../lib/entityLabels.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import CopyableIdentifier from '../../components/CopyableIdentifier.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import EntityRefList from '../../components/EntityRefList.svelte';
  import RelationshipList from '../../components/RelationshipList.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';
  import { formatDate } from '../../lib/dateFormat.ts';

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
  // "immutable" just because the revision is known. Anything that lives in the bundle
  // FILES rather than in the contract (the raw OpenAPI document, JSON Schema bodies,
  // doc and skill bodies, the SBOM) is not retained by the snapshot, so this page
  // shows the declared PATH to it and never pretends to have read it.
  let { detail } = $props();
  const d = $derived(detail.revision ?? {});
  const id = $derived(d.identity ?? {});
  const r = $derived(d.readiness ?? null);
  const o = $derived(d.ownership ?? null);
  const prov = $derived(d.provenance ?? {});
  const state = $derived(d.state ?? null);
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
        {#if o.ref}<EntityLink ref={o.ref} showStatus={false} showKind={false} />{:else}<span>{o.owner || 'Unowned'}</span>{/if}
      </div>
    {/if}
  </section>

  {#if id.digest || id.resolvedRef || id.requestedRef}
    <section class="re-identity">
      {#if id.digest}<div class="re-idrow"><span class="re-k">Digest</span><CopyableIdentifier value={id.digest} /></div>{/if}
      {#if id.resolvedRef}<div class="re-idrow"><span class="re-k">Resolved ref</span><CopyableIdentifier value={id.resolvedRef} /></div>{/if}
      {#if id.requestedRef && id.requestedRef !== id.resolvedRef}<div class="re-idrow"><span class="re-k">Requested ref</span><CopyableIdentifier value={id.requestedRef} /></div>{/if}
    </section>
  {/if}

  {#if r}
    <section class="re-readiness">
      <div class="rr-head">
        <h2>Readiness</h2>
        <IdentityBadge label={r.passing ? 'Passing' : 'Not passing'} tone={r.passing ? 'ok' : 'warn'} />
      </div>
      <!-- The distinction a first-time user cannot guess from the word "readiness":
           this is the contract's own self-assessment of how prepared this revision is,
           scored against a gate its authors declared. It says nothing about whether the
           running system obeys the contract -- that is compliance, shown on the targets. -->
      <p class="rr-lead">What the authors declared about this revision's preparedness — not a measurement of the running system.</p>
      <p class="rr-line">Score {r.score} / {r.minScore} required · {r.doneCount} done · {r.partialCount} partial · {r.notDoneCount} not done{r.deferredCount ? ` · ${r.deferredCount} deferred` : ''}{r.expired ? ' · expired' : ''}</p>
      {#if (r.checks?.count ?? 0) > 0}
        <PreviewSection title="Readiness checks" level={3} total={r.checks?.total ?? 0} count={r.checks?.count ?? 0} truncated={r.checks?.truncated}>
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
    <section class="re-readiness">
      <div class="rr-head">
        <h2>Readiness</h2>
        <IdentityBadge label="Not declared" tone="neutral" />
      </div>
      <p class="rr-lead">This revision declares no readiness gate, so there is nothing here to pass or fail — which is not the same as failing one.</p>
    </section>
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
                 so this is the real API surface, not a restatement of the declaration. -->
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
          {#if c.ref}<div class="ri-ref"><span class="re-k">Reference</span><CopyableIdentifier value={c.ref} /></div>{/if}
          {#if (c.values?.count ?? 0) > 0}
            <table class="re-kv">
              <thead><tr><th scope="col">Key</th><th scope="col">Value</th></tr></thead>
              <tbody>
                {#each c.values.items as v (v.key)}<tr><td>{v.key}</td><td>{v.value}</td></tr>{/each}
              </tbody>
            </table>
            {#if c.values.truncated}<p class="ri-note">Showing {c.values.count} values{typeof c.values.total === 'number' ? ` of ${c.values.total}` : '; total unknown'}.</p>{/if}
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
            <td data-label="Definition" class="rt-path">{p.schema || p.ref || '—'}</td>
            <td data-label="Target">{p.target || '—'}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </PreviewSection>

  <PreviewSection
    title="Capabilities"
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

  {#if (d.validation?.count ?? 0) > 0}
    <PreviewSection title="Validation findings" total={d.validation?.total ?? 0} count={d.validation?.count ?? 0} truncated={d.validation?.truncated}>
      <FindingList items={d.validation?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.dependencies?.count ?? 0) > 0}
    <PreviewSection title="Declared dependencies" total={d.dependencies?.total ?? null} count={d.dependencies?.count ?? 0} truncated={d.dependencies?.truncated}>
      <RelationshipList items={d.dependencies?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.tools?.count ?? 0) > 0}
    <!-- The same operations, flat and cross-interface: this is the agent-facing tool
         list (the names MCP exposes), and it is also the safety net that keeps an
         operation reachable if the interface list above is truncated. -->
    <PreviewSection title="Tools exposed to agents" total={d.tools?.total ?? 0} count={d.tools?.count ?? 0} truncated={d.tools?.truncated}>
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
    <PreviewSection title="Skills" total={d.skills?.total ?? 0} count={d.skills?.count ?? 0} truncated={d.skills?.truncated}>
      <ul class="re-chips">{#each d.skills.items as s (s)}<li>{s}</li>{/each}</ul>
    </PreviewSection>
  {/if}

  {#if (d.docs?.count ?? 0) > 0}
    <PreviewSection title="Docs" total={d.docs?.total ?? 0} count={d.docs?.count ?? 0} truncated={d.docs?.truncated}>
      <ul class="re-docs">{#each d.docs.items as doc (doc.path)}<li><span class="rd-title">{doc.title || doc.path}</span>{#if doc.title && doc.path}<span class="rd-path">{doc.path}</span>{/if}</li>{/each}</ul>
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

  {#if d.previous || d.next}
    <section class="re-adjacent">
      {#if d.previous}<div class="re-adj"><span class="re-k">Previous revision</span><EntityLink ref={d.previous} showStatus={false} showKind={false} /></div>{/if}
      {#if d.next}<div class="re-adj"><span class="re-k">Next revision</span><EntityLink ref={d.next} showStatus={false} showKind={false} /></div>{/if}
    </section>
  {/if}

  {#if prov.source || (prov.sources?.count ?? 0) > 0 || prov.fetchedAt}
    <!-- Where this content came from. The identity block above says WHAT this revision
         is; this says who told us, and when we last heard it. Both are needed to answer
         "is what I am reading still what the registry has?". -->
    <section class="re-facts">
      {#if prov.source}<div class="re-fact"><span class="re-k">Primary source</span><span>{prov.source}</span></div>{/if}
      {#if (prov.sources?.count ?? 0) > 0}
        <div class="re-fact">
          <span class="re-k">Seen by</span>
          <span>{prov.sources.items.join(', ')}{prov.sources.truncated ? ` (${prov.sources.count} of ${prov.sources.total})` : ''}</span>
        </div>
      {/if}
      {#if prov.fetchedAt}<div class="re-fact"><span class="re-k">Fetched at</span><span>{formatDate(prov.fetchedAt)}</span></div>{/if}
    </section>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .rev-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .re-facts, .re-identity, .re-adjacent { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .re-fact, .re-idrow, .re-adj { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .re-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .re-readiness { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .rr-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .rr-head h2 { margin: 0; font-size: var(--text-md); }
  .rr-line { color: var(--c-text-2); font-size: var(--text-sm); margin: var(--sp-2) 0 0; }
  .rr-lead { color: var(--c-text-3); font-size: var(--text-sm); margin: 0; }
  .re-checks, .re-tools, .re-docs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .re-checks li, .re-tools li, .re-docs li { display: flex; gap: var(--sp-2); align-items: baseline; flex-wrap: wrap; font-size: var(--text-sm); }
  .rc-id, .rt-name { color: var(--c-text); }
  .rc-cat, .rc-desc, .rt-summary, .rd-path { color: var(--c-text-3); }
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
