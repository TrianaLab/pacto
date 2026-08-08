<script>
  import {
    linkStateLabel, linkStateTone, retrievabilityLabel, retrievabilityTone,
  } from '../../lib/entityLabels.ts';
  import { formatDate } from '../../lib/dateFormat.ts';
  import EntityLink from '../../components/EntityLink.svelte';
  import StatusBadge from '../../components/StatusBadge.svelte';
  import IdentityBadge from '../../components/IdentityBadge.svelte';
  import PreviewSection from '../../components/PreviewSection.svelte';
  import FindingList from '../../components/FindingList.svelte';
  import LimitationsList from '../../components/LimitationsList.svelte';

  // The deployment/target page (requirement F). It makes the TWO identity dimensions
  // immediately understandable and independent:
  //   1. revision-match certainty (linkState: exact / inferred / ambiguous / unresolved)
  //   2. content retrievability (identity: exact retrievable / mutable / no-ref / local /
  //      malformed / digest-mismatch)
  // An exact match to non-retrievable content is VALID and never rendered as
  // contradictory (they are separate badges with independent tones). An
  // ambiguous/unresolved match NEVER presents a specific revision as authoritative.
  let { detail } = $props();
  const d = $derived(detail.target ?? {});
  const id = $derived(d.identity ?? {});
  // A specific revision is authoritative ONLY when the match is exact or inferred.
  const authoritativeRevision = $derived(
    (d.linkState === 'exact' || d.linkState === 'inferred') ? d.revision : null,
  );
  const unresolvedMatch = $derived(d.linkState === 'ambiguous' || d.linkState === 'unresolved');
</script>

<div class="tgt-entity">
  <!-- The two orthogonal identity dimensions, side by side. -->
  <section class="te-identity" aria-label="Deployment identity">
    <div class="te-dim">
      <span class="te-k">Revision match</span>
      <IdentityBadge label={linkStateLabel(d.linkState)} tone={linkStateTone(d.linkState)} />
    </div>
    <div class="te-dim">
      <span class="te-k">Content</span>
      <IdentityBadge label={retrievabilityLabel(id.identityClass, id.retrievable)} tone={retrievabilityTone(id.identityClass, id.retrievable)} />
    </div>
  </section>

  {#if authoritativeRevision}
    <div class="te-rev">
      <span class="te-k">{d.linkState === 'exact' ? 'Running revision' : 'Inferred revision'}</span>
      <EntityLink ref={authoritativeRevision} showStatus={false} />
      {#if d.linkState === 'inferred'}<span class="te-note">match inferred, not certain</span>{/if}
    </div>
  {:else if unresolvedMatch}
    <div class="te-rev te-rev-unresolved" role="status">
      No single revision is authoritative for this deployment ({linkStateLabel(d.linkState).toLowerCase()}); it is not attributed to a specific revision.
    </div>
  {/if}

  <section class="te-facts">
    <div class="te-fact"><span class="te-k">Service</span><EntityLink ref={d.service} showStatus={false} /></div>
    {#if d.scope}<div class="te-fact"><span class="te-k">Scope</span><span>{d.scope}</span></div>{/if}
    {#if d.kind}<div class="te-fact"><span class="te-k">Target kind</span><span>{d.kind}</span></div>{/if}
    <div class="te-fact"><span class="te-k">Compliance</span><StatusBadge status={d.compliance} /></div>
    {#if d.coverage}<div class="te-fact"><span class="te-k">Evaluation coverage</span><span>{d.coverage.evaluated} of {d.coverage.required} evaluated</span></div>{/if}
    {#if d.source}<div class="te-fact"><span class="te-k">Source</span><span>{d.source}</span></div>{/if}
    {#if (d.sources?.count ?? 0) > 0}<div class="te-fact"><span class="te-k">Contributing sources</span><span>{d.sources.items.join(', ')}{d.sources.truncated ? ` (+${d.sources.total - d.sources.count})` : ''}</span></div>{/if}
    {#if d.evidenceAt}<div class="te-fact"><span class="te-k">Evidence at</span><span>{formatDate(d.evidenceAt)}</span></div>{/if}
    {#if d.reconciledAt}<div class="te-fact"><span class="te-k">Reconciled at</span><span>{formatDate(d.reconciledAt)}</span></div>{/if}
    {#if d.stale}<div class="te-fact"><IdentityBadge label="Evidence stale" tone="warn" /></div>{/if}
    {#if d.quarantined}<div class="te-fact"><IdentityBadge label="Quarantined" tone="err" /></div>{/if}
  </section>

  {#if (d.findings?.count ?? 0) > 0}
    <PreviewSection title="Findings" total={d.findings?.total ?? 0} count={d.findings?.count ?? 0} truncated={d.findings?.truncated}>
      <FindingList items={d.findings?.items ?? []} />
    </PreviewSection>
  {/if}

  {#if (d.observedRuntime?.count ?? 0) > 0}
    <PreviewSection title="Observed runtime" total={d.observedRuntime?.total ?? d.observedRuntime?.scanned ?? d.observedRuntime?.count ?? 0} count={d.observedRuntime?.count ?? 0} truncated={d.observedRuntime?.truncated}>
      <ul class="te-runtime">
        {#each d.observedRuntime.items as f, i (i)}
          <li><span class="rt-key">{f.key}</span><span class="rt-val">{f.value}</span></li>
        {/each}
      </ul>
    </PreviewSection>
  {/if}

  {#if d.ownership}
    <section class="te-facts">
      <div class="te-fact">
        <span class="te-k">Owner</span>
        {#if d.ownership.ref}<EntityLink ref={d.ownership.ref} showStatus={false} />{:else}<span>{d.ownership.owner || 'Unowned'}</span>{/if}
      </div>
    </section>
  {/if}

  {#if (d.limitations?.count ?? 0) > 0}
    <PreviewSection title="Limitations" total={d.limitations?.total ?? 0} count={d.limitations?.count ?? 0} truncated={d.limitations?.truncated}>
      <LimitationsList items={d.limitations?.items ?? []} />
    </PreviewSection>
  {/if}
</div>

<style>
  .tgt-entity { display: flex; flex-direction: column; gap: var(--sp-4); }
  .te-identity { display: flex; gap: var(--sp-6); flex-wrap: wrap; padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-md); background: var(--c-surface); }
  .te-dim { display: flex; flex-direction: column; gap: var(--sp-1); }
  .te-facts { display: flex; gap: var(--sp-5); flex-wrap: wrap; }
  .te-fact, .te-rev { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .te-k { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .te-note { font-size: var(--text-xs); color: var(--c-text-3); }
  .te-rev-unresolved { padding: var(--sp-2) var(--sp-3); border-radius: var(--radius-sm); background: var(--c-surface-inset); border: 1px solid var(--c-border); color: var(--c-text-2); font-size: var(--text-sm); }
  .te-runtime { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .te-runtime li { display: flex; gap: var(--sp-3); font-size: var(--text-sm); }
  .rt-key { color: var(--c-text-3); font-family: var(--font-mono, monospace); min-width: 40%; overflow-wrap: anywhere; }
  .rt-val { color: var(--c-text); overflow-wrap: anywhere; }
</style>
