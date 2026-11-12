<script>
  import PreviewSection from './PreviewSection.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import ShowAll from './ShowAll.svelte';
  import Glyph from './figure/Glyph.svelte';
  import EnvMark from './figure/EnvMark.svelte';
  import { environmentBadge, lifetimeBadge, expiryText, ownerLabel } from '../lib/behavior.ts';
  import { PHASE_WORDS, phaseTone } from '../lib/provisioning.ts';
  import { timeLabel, andList } from '../lib/delivery.ts';
  import { fleetEntityListUrl, deliveryHistoryUrl, hashForHref } from '../lib/router.ts';
  import { cap } from '../lib/cap.svelte.ts';

  // A service's environments on its page (#467): one row per environment the current
  // revision declares or a target you can see reports, joined by exact name. Each fact
  // says where it comes from: the Declared cell is the contract, the Observed cell the
  // targets, the Provisioning cell what providers report, and the Stage cell the Stages
  // an admin declared (#466's rows, bound out of the Delivery section, never refetched).
  // An unknown never reads as a positive: a lifetime nobody declared is not declared,
  // an expiry is inferred against `asOf`, a hidden target qualifies its count and an
  // untracked environment shows no zeros.
  // `service` is the canonical service key the target list filters by.
  /** @type {{ placement?: import('../lib/api.ts').ProductPlacementPreview | null, history?: import('../lib/api.ts').EnvironmentHistory | null,
   *   stageRows?: import('../lib/api.ts').EnvironmentRow[], stagesState?: string, stagesCompleteness?: string, asOf?: string, completeness?: string, service?: string }} */
  let { placement = null, history = null, stageRows = [], stagesState = '', stagesCompleteness = '', asOf = '', completeness = '', service = '' } = $props();

  const items = $derived(placement?.items ?? []);
  const current = $derived(placement?.currentRevision?.version || placement?.currentRevision?.label || 'the current revision');
  const staged = $derived(stagesState === 'served');
  const stagesOf = (env) => stageRows.filter((r) => r.environment === env);
  // A Stage whose environment matches no group: not declared and no visible target reports it.
  const stageOnly = $derived.by(() => {
    if (!staged) return [];
    const names = new Set(items.map((g) => g.environment));
    return [...new Set(stageRows.map((r) => r.environment))].filter((e) => !names.has(e));
  });
  const declaredNames = $derived(items.filter((g) => g.declaration).map((g) => g.environment));
  const differences = $derived(placement?.differences ?? []);
  /** @param {import('../lib/api.ts').EnvironmentDifference} d */
  const diffEnvs = (d) => [...new Set([...declaredNames, ...Object.keys(d.values ?? {})])];
  const BUCKETS = ['ready', 'failed', 'provisioning', 'requested', 'deleting', 'unknown'];
  const phaseOf = (k) => k[0].toUpperCase() + k.slice(1);
  const tallyTotal = (t) => BUCKETS.reduce((n, k) => n + (t?.[k] ?? 0), 0);
  /** @type {Record<string, string>} */
  const EVENT = { added: 'added in', removed: 'removed in', changed: 'changed in' };
  const events = $derived(history?.items ?? []);
  const groups = cap(25);
  const paths = cap(10);
  const hist = cap(25);
  // Stage-only rows count too, so the heading never lists more rows than it counts.
  const rowCount = $derived(items.length + stageOnly.length);
</script>

{#snippet stageLink(r)}
  <span class="sv-line">
    <EnvMark state={r.state} word />
    <a href={deliveryHistoryUrl({ namespace: r.namespace, stage: r.stage })} title={`Stage ${r.namespace}/${r.stage}`}><bdi>Stage {r.namespace}/{r.stage}</bdi></a>
    <span class="t-meta">state observed, environment declared by a Stage (admin)</span>
  </span>
{/snippet}

<!-- clip={false}: the rows cap themselves (Show all below), and the differences, history
     and the asOf/completeness line under them must never hide behind the section's fade. -->
<PreviewSection title="Environments" count={rowCount} total={(placement?.total ?? items.length) + stageOnly.length} clip={false} {emptyBody}
  help="Each environment the current revision declares or a target you can see reports: its declaration, the targets observed there, provisioning and Stages.">
  <ul class="sv-list" class:sv-staged={staged}>
    {#each groups.slice(items) as g (g.environment)}
      {@const d = g.declaration}
      {@const life = lifetimeBadge(d?.lifetime)}
      {@const values = environmentBadge(d?.status)}
      {@const expiry = d ? expiryText(d, asOf) : ''}
      {@const total = tallyTotal(g.provisioning)}
      {@const stages = stagesOf(g.environment)}
      <li class="sv-row" data-testid="service-environment" data-env={g.environment}>
        <div class="sv-cell sv-name" data-cell="name">
          <span class="sv-line"><Glyph kind="environment" /><bdi title={g.environment || '(no environment label)'}>{g.environment || '(no environment label)'}</bdi></span>
          {#if d}<span class="badge badge-ok">Declared</span>
          {:else if g.environment}<span class="badge badge-warn">Not declared</span>
          {:else}<span class="badge badge-neutral"><span class="sv-hatch hatch" aria-hidden="true"></span>Unknown</span>{/if}
        </div>
        <div class="sv-cell" data-cell="declared">
          <span class="sv-k">Declared</span>
          {#if d}
            <span class="sv-line">
              <IdentityBadge label={`Values ${values.label.toLowerCase()}`} tone={values.tone} title={d.reason || d.file} />
              <IdentityBadge label={life.label} tone={life.tone} title={life.title} />
            </span>
            {#if expiry}<span class="sv-line" class:sv-warn={d.expired}>{expiry}</span>{/if}
            <span class="sv-line" title={g.owner?.reason || undefined}>{ownerLabel(g.owner)}</span>
          {:else if g.environment}
            <span class="sv-line">Not declared by <bdi>{current}</bdi></span>
            <span class="sv-line t-meta">{ownerLabel(g.owner)}</span>
          {:else}
            <span class="sv-line t-meta">Unknown: these targets report no environment label or their sources disagree</span>
          {/if}
        </div>
        <div class="sv-cell" data-cell="observed">
          <span class="sv-k">Observed</span>
          <span class="sv-line">
            {#if g.environment}<a href={fleetEntityListUrl('target', { service, environment: g.environment })}>Targets you can see: {g.targets}</a>{:else}<span>Targets you can see: {g.targets}</span>{/if}
            {#if !g.targetsComplete}<span>(incomplete)</span>{/if}
          </span>
          {#if !g.targetsComplete && g.targetsReason}<span class="sv-line t-meta">{g.targetsReason}</span>{/if}
          {#if g.declared || g.notDeclared || g.unknown}
            <span class="sv-line">
              {#if g.declared}<span class="badge badge-ok">{g.declared} declared</span>{/if}
              {#if g.notDeclared}<span class="badge badge-warn">{g.notDeclared} not declared</span>{/if}
              {#if g.unknown}<span class="badge badge-neutral"><span class="sv-hatch hatch" aria-hidden="true"></span>{g.unknown} unknown</span>{/if}
            </span>
          {/if}
          {#if g.revisions?.length}
            <span class="sv-line">
              {#each g.revisions as v (v.key)}<a href={hashForHref(v.href)} title={v.version || v.label}><bdi>{v.version || v.label}</bdi></a>{/each}
              {#if g.revisionsTruncated}<span class="t-meta">and {g.revisionsTotal - g.revisions.length} more</span>{/if}
            </span>
          {/if}
        </div>
        <div class="sv-cell" data-cell="provisioning">
          <span class="sv-k">Provisioning</span>
          {#if !g.provisioningTracked}
            <span class="sv-line t-meta">Provisioning not tracked: {g.provisioningReason || 'no provider is asked about it'}</span>
          {:else if !total}
            <span class="sv-line t-meta">No offering use asks for a resource here</span>
          {:else if g.provisioning.unknown === total}
            <span class="sv-line t-meta">Unknown until a provider reports</span>
          {:else}
            <span class="sv-line">
              {#each BUCKETS.filter((k) => g.provisioning[k]) as k (k)}<IdentityBadge label={`${PHASE_WORDS[k]} ${g.provisioning[k]}`} tone={phaseTone(phaseOf(k))} />{/each}
            </span>
          {/if}
        </div>
        {#if staged}
          <div class="sv-cell" data-cell="stage">
            <span class="sv-k">Stage</span>
            {#if stages.length}
              {#each stages as r (`${r.namespace}/${r.stage}`)}{@render stageLink(r)}{/each}
            {:else if stagesCompleteness === 'partial'}
              <span class="sv-line t-meta">Stages unknown: partial listing</span>
            {:else if g.environment}
              <span class="sv-line t-meta">No Stage you can see</span>
            {:else}
              <span class="sv-line t-meta">Unknown: these targets report no environment to match a Stage</span>
            {/if}
          </div>
        {/if}
      </li>
    {/each}
    {#each stageOnly as env (env)}
      <li class="sv-row" data-testid="service-environment" data-env={env}>
        <div class="sv-cell sv-name" data-cell="name">
          <span class="sv-line"><Glyph kind="environment" /><bdi title={env}>{env}</bdi></span>
          <span class="badge badge-warn">Not declared</span>
        </div>
        <div class="sv-cell sv-wide" data-cell="declared">
          <span class="sv-line sv-warn">Stage only: not declared by <bdi>{current}</bdi>, no target you can see reports it</span>
        </div>
        <div class="sv-cell" data-cell="stage">
          <span class="sv-k">Stage</span>
          {#each stagesOf(env) as r (`${r.namespace}/${r.stage}`)}{@render stageLink(r)}{/each}
        </div>
      </li>
    {/each}
  </ul>
  <ShowAll cap={groups} total={items.length} noun="environments" />
  {#if placement?.truncated}<p class="t-meta">And {placement.total - placement.count} more environments.</p>{/if}
  {@render after()}
</PreviewSection>

{#snippet emptyBody()}
  <p class="sv-empty t-body-2" data-testid="environments-empty">The current revision declares no environment and no target you can see reports one{completeness === 'partial' ? '; this answer is partial, so one may exist' : ''}.</p>
  {@render after()}
{/snippet}

{#snippet after()}
  {#if declaredNames.length >= 2 || placement?.differencesError}
    <section class="sv-sub" data-testid="environment-differences" aria-labelledby="sv-diff-h">
      <h3 id="sv-diff-h" class="t-subsection-title">Differences between environments</h3>
      {#if placement?.differencesError}
        <p class="t-meta sv-warn">Differences unknown: {placement.differencesError}</p>
      {:else if !differences.length}
        <p class="t-meta">The declared environments set the same values.</p>
      {:else}
        <p class="t-meta">Each path is a value the declared environments disagree on.</p>
        {#each paths.slice(differences) as p (`${p.configuration}|${p.path}`)}
          <div class="sv-diff">
            <p class="sv-diff-h"><span>{p.configuration}</span> <code title={p.path}>{p.path}</code></p>
            <ul class="sv-diff-rows">
              {#each diffEnvs(p) as env (env)}
                <li class="sv-diff-row" data-testid="difference-row"><bdi title={env}>{env}</bdi> {#if env in p.values}<code title={JSON.stringify(p.values[env])}>{JSON.stringify(p.values[env])}</code>{:else}<span class="t-meta">not set</span>{/if}</li>
              {/each}
            </ul>
          </div>
        {/each}
        <ShowAll cap={paths} total={differences.length} noun="paths" />
        {#if (placement?.differencesTotal ?? 0) > differences.length}<p class="t-meta">{placement.differencesTotal - differences.length} more</p>{/if}
      {/if}
    </section>
  {/if}

  {#if history}
    <section class="sv-sub" data-testid="environment-history" aria-labelledby="sv-hist-h">
      <h3 id="sv-hist-h" class="t-subsection-title">Changes between versions (version order)</h3>
      {#if events.length}
        <ul class="sv-events">
          {#each hist.slice(events) as e, i (i)}
            <li class="sv-line" data-testid="environment-event">
              <bdi class="sv-env" title={e.environment}>{e.environment}</bdi>
              <span>{e.change === 'present' ? 'declared since at least' : `${e.fields?.length ? `${andList(e.fields, 4)} ` : ''}${EVENT[e.change] ?? e.change}`}</span>
              <a href={hashForHref(e.revision.href)}><bdi>{e.revision.version || e.revision.label}</bdi></a>
              {#if e.previous}<span class="t-meta">compared with</span>{' '}<a href={hashForHref(e.previous.href)}><bdi>{e.previous.version || e.previous.label}</bdi></a>{/if}
              <span class="t-meta">time not recorded</span>
            </li>
          {/each}
        </ul>
        <ShowAll cap={hist} total={events.length} noun="changes" />
        {#if history.truncated}<p class="t-meta">{history.total - history.count} more</p>{/if}
      {:else}
        <p class="t-meta">No declared environment changed between the versions Pacto holds.</p>
      {/if}
      {#if history.gaps > 0}<p class="t-meta sv-warn">{history.gaps} {history.gaps === 1 ? 'revision' : 'revisions'} could not be read; changes across them are unknown</p>{/if}
      {#if history.completeness === 'partial'}<p class="t-meta">{history.completenessReason || 'Pacto holds only some revisions of this service.'}</p>{/if}
    </section>
  {/if}

  <p class="t-meta sv-foot" data-testid="environments-footer">As of {timeLabel(asOf) || 'unknown'}, {completeness || 'completeness unknown'}</p>
{/snippet}

<style>
  /* One row per environment: four or five labelled cells side by side on a wide
     container, a stacked card below it (SPEC 24). Long values wrap, never scroll. */
  .sv-list { container-type: inline-size; list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; }
  .sv-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: var(--sp-1) var(--sp-3); padding: var(--sp-2) 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; overflow-wrap: anywhere; }
  @container (min-width: 56rem) {
    .sv-row { grid-template-columns: minmax(14ch, 1fr) minmax(20ch, 1.6fr) minmax(16ch, 1.3fr) minmax(16ch, 1.2fr); align-items: start; }
    .sv-staged .sv-row { grid-template-columns: minmax(14ch, 1fr) minmax(20ch, 1.6fr) minmax(16ch, 1.3fr) minmax(16ch, 1.2fr) minmax(16ch, 1.3fr); }
    .sv-wide { grid-column: 2 / span 3; }
  }
  .sv-cell { display: flex; flex-direction: column; gap: var(--sp-1); min-inline-size: 0; }
  .sv-name { font-weight: 600; }
  .sv-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .sv-line { display: flex; flex-wrap: wrap; align-items: baseline; gap: var(--sp-1) var(--sp-2); min-inline-size: 0; overflow-wrap: anywhere; }
  .sv-line bdi { min-inline-size: 0; overflow-wrap: anywhere; }
  .sv-warn { color: var(--c-warn); }
  .sv-hatch { display: inline-block; inline-size: 0.75em; block-size: 0.75em; margin-inline-end: var(--sp-1); border-radius: 2px; vertical-align: middle; }
  .sv-sub { display: flex; flex-direction: column; gap: var(--sp-1); margin-block-start: var(--sp-3); min-inline-size: 0; }
  .sv-sub h3 { margin: 0; }
  /* Differences: one row per environment under its path, so 200 environments add rows, never columns. */
  .sv-diff { container-type: inline-size; min-inline-size: 0; }
  .sv-diff-h { margin: var(--sp-2) 0 0; font-weight: 600; overflow-wrap: anywhere; }
  .sv-diff-rows { list-style: none; margin: 0; padding: 0; }
  .sv-diff-row { display: grid; grid-template-columns: minmax(0, 1fr); gap: 0 var(--sp-3); padding: 2px 0; border-block-end: 1px solid var(--c-border); min-inline-size: 0; overflow-wrap: anywhere; }
  @container (min-width: 36rem) { .sv-diff-row { grid-template-columns: minmax(14ch, 1fr) minmax(16ch, 2fr); } }
  .sv-diff-row code, .sv-diff-h code { overflow-wrap: anywhere; min-inline-size: 0; }
  .sv-events { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-1); }
  .sv-env { font-weight: 600; }
  .sv-foot { margin: var(--sp-3) 0 0; }
  .sv-empty { margin: 0; }
  @media (pointer: coarse) { .sv-row a, .sv-events a { display: inline-flex; align-items: center; min-block-size: 44px; } }
</style>
