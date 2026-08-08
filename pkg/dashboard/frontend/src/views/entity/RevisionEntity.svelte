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

  // The revision page (requirement E): an IMMUTABLE version of the service contract,
  // not a deployment. It renders the contract facets (interfaces/configs/policies/
  // capabilities), ownership, readiness (score AND the derived checks), declared
  // dependencies, the bounded tools / skills / docs the contract exposes, validation,
  // exact + inferred targets, and previous/next revisions. Content retrievability is
  // shown as its OWN dimension: the revision is immutable, but its content may not be
  // retrievable -- we never call non-retrievable content "immutable" just because the
  // revision is known. Every collection is rendered as a truncation-honest
  // PreviewSection from the already-available bounded EntityDetail payload (requirement
  // F item 2): nothing here is a bare count badge that hides its contents.
  let { detail } = $props();
  const d = $derived(detail.revision ?? {});
  const id = $derived(d.identity ?? {});
  const facets = $derived([
    { k: 'Interfaces', v: d.interfaces ?? 0 },
    { k: 'Configurations', v: d.configurations ?? 0 },
    { k: 'Policies', v: d.policies ?? 0 },
    { k: 'Capabilities', v: d.capabilities ?? 0 },
  ]);
  const r = $derived(d.readiness ?? null);
  const o = $derived(d.ownership ?? null);
</script>

<div class="rev-entity">
  <section class="re-facts">
    <div class="re-fact"><span class="re-k">Service</span><EntityLink ref={d.service} showStatus={false} /></div>
    {#if d.version}<div class="re-fact"><span class="re-k">Version</span><span>{d.version}</span></div>{/if}
    {#if d.pactoVersion}<div class="re-fact"><span class="re-k">pactoVersion</span><span>{d.pactoVersion}</span></div>{/if}
    <div class="re-fact"><span class="re-k">Valid</span><span>{d.valid ? 'Yes' : 'No'}</span></div>
    <div class="re-fact"><span class="re-k">Content</span><IdentityBadge label={retrievabilityLabel(id.identityClass, id.retrievable)} tone={retrievabilityTone(id.identityClass, id.retrievable)} /></div>
    {#if o}
      <div class="re-fact">
        <span class="re-k">Owner</span>
        {#if o.ref}<EntityLink ref={o.ref} showStatus={false} />{:else}<span>{o.owner || 'Unowned'}</span>{/if}
      </div>
    {/if}
  </section>

  {#if o?.conflicts?.count}
    <div class="re-conflicts" role="status">
      <strong>Ownership conflict.</strong>
      <span>Revisions of this service declare different owners: {o.conflicts.items.join(', ')}{o.conflicts.truncated ? ` (+${o.conflicts.total - o.conflicts.count} more)` : ''}.</span>
    </div>
  {/if}

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
      <p class="rr-line">Score {r.score} / {r.minScore} required · {r.doneCount} done · {r.partialCount} partial · {r.notDoneCount} not done{r.deferredCount ? ` · ${r.deferredCount} deferred` : ''}{r.expired ? ' · expired' : ''}</p>
      {#if (r.checks?.count ?? 0) > 0}
        <PreviewSection title="Readiness checks" total={r.checks?.total ?? 0} count={r.checks?.count ?? 0} truncated={r.checks?.truncated}>
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
  {/if}

  <div class="re-facets">
    {#each facets as f}<span class="re-facet"><b>{f.v}</b> {f.k}</span>{/each}
  </div>

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
    <PreviewSection title="Tools" total={d.tools?.total ?? 0} count={d.tools?.count ?? 0} truncated={d.tools?.truncated}>
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
      <PreviewSection title="Exact-match deployments" total={d.exactTargets?.total ?? 0} count={d.exactTargets?.count ?? 0} truncated={d.exactTargets?.truncated}>
        <EntityRefList items={d.exactTargets?.items ?? []} />
      </PreviewSection>
    {/if}
    {#if (d.inferredTargets?.count ?? 0) > 0}
      <PreviewSection title="Inferred-match deployments" total={d.inferredTargets?.total ?? 0} count={d.inferredTargets?.count ?? 0} truncated={d.inferredTargets?.truncated}>
        <EntityRefList items={d.inferredTargets?.items ?? []} />
      </PreviewSection>
    {/if}
  </div>

  {#if d.previous || d.next}
    <section class="re-adjacent">
      {#if d.previous}<div class="re-adj"><span class="re-k">Previous revision</span><EntityLink ref={d.previous} showStatus={false} /></div>{/if}
      {#if d.next}<div class="re-adj"><span class="re-k">Next revision</span><EntityLink ref={d.next} showStatus={false} /></div>{/if}
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
  .re-conflicts {
    display: flex; gap: var(--sp-2); flex-wrap: wrap; align-items: baseline;
    padding: var(--sp-3); border-radius: var(--radius-md); font-size: var(--text-sm);
    background: var(--c-warn-bg); border: 1px solid var(--c-warn-border);
  }
  .re-readiness { border: 1px solid var(--c-border); border-radius: var(--radius-md); padding: var(--sp-3); background: var(--c-surface); display: flex; flex-direction: column; gap: var(--sp-3); }
  .rr-head { display: flex; align-items: baseline; gap: var(--sp-3); }
  .rr-head h2 { margin: 0; font-size: var(--text-md); }
  .rr-line { color: var(--c-text-2); font-size: var(--text-sm); margin: var(--sp-2) 0 0; }
  .re-checks, .re-tools, .re-docs { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .re-checks li, .re-tools li, .re-docs li { display: flex; gap: var(--sp-2); align-items: baseline; flex-wrap: wrap; font-size: var(--text-sm); }
  .rc-id, .rt-name { color: var(--c-text); }
  .rc-cat, .rc-desc, .rt-summary, .rd-path { color: var(--c-text-3); }
  .rt-method { font-family: var(--font-mono, monospace); text-transform: uppercase; color: var(--c-text-2); }
  .rt-path { font-family: var(--font-mono, monospace); color: var(--c-text); overflow-wrap: anywhere; }
  .re-chips { list-style: none; margin: 0; padding: 0; display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .re-chips li { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); padding: 2px 10px; border-radius: var(--radius-xs); }
  .re-facets { display: flex; gap: var(--sp-2); flex-wrap: wrap; }
  .re-facet { font-size: var(--text-sm); color: var(--c-text-2); background: var(--c-surface-inset); border: 1px solid var(--c-border); padding: 2px 10px; border-radius: var(--radius-xs); }
  .re-facet b { color: var(--c-text); }
  .re-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: var(--sp-3); }
</style>
