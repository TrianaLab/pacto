<script>
  // The revision page's figures (SPEC 7): where validation stopped (a Rail of the stages
  // in run order, walked one station at a time, A1b), the revision between its
  // neighbours (a Stack: the next one above, this one, the previous one below) with its
  // place in the service and its share of the service's targets, and where the evidence
  // about it comes from (its sources wired into it in their state's style).
  import Figure from './Figure.svelte';
  import Rail from './Rail.svelte';
  import Node from './Node.svelte';
  import HubNode from './HubNode.svelte';
  import StackRows from './StackRows.svelte';
  import { validationModel, revisionStackModel, evidenceModel } from '../../lib/entityFigures.ts';
  import { inService } from '../../lib/identityContext.ts';
  import { countOf, formatCount } from '../../lib/format.ts';
  import { fleetEntityListUrl, fleetEntityUrl } from '../../lib/router.ts';
  import { elidable } from '../../lib/elide.ts';

  /** @type {{ stages: any[], revision: any, self: string, service: string, historyHref: string }} */
  let { stages, revision, self, service, historyHref } = $props();
  const v = $derived(validationModel(stages));
  const label = (/** @type {any} */ r) => inService(r, service).label ?? r.key ?? '';
  const s = $derived(revisionStackModel({ key: self, version: revision.version, label: revision.version || self }, revision.previous, revision.next, label));
  const running = $derived((revision.exactTargets?.total ?? 0) + (revision.inferredTargets?.total ?? 0));
  const targetsHref = $derived(revision.service?.key ? fleetEntityListUrl('target', { service: revision.service.key }) : '');
  const ev = $derived(evidenceModel(revision.evidenceSources, revision.evidenceSourceCounts));
  const evc = $derived(revision.evidenceSourceCounts ?? {});
  const evOther = $derived((evc.total ?? 0) - (evc.available ?? 0) - (evc.stale ?? 0) - (evc.partial ?? 0) - (evc.unavailable ?? 0));
  const version = $derived(revision.version || self);
</script>

{#if stages.length}
  <Figure id="validation" testid="revision-validation-figure" question={v.question} alt={v.alt} wires={v.wires} steps={v.steps} walk
    rowMin={v.stations.length * 150} autoplay={v.autoplay} autoKey={v.key}>
    {#snippet stage()}<Rail stations={v.stations} />{/snippet}
    {#snippet caption()}{v.summary}{/snippet}
  </Figure>
{/if}

<Figure id="around" testid="revision-around-figure" narrow question="What is around this revision?" alt={s.alt} wires={s.wires}>
  {#snippet stage()}<StackRows layers={s.layers} rows={s.rows} />{/snippet}
  {#snippet aside()}
    <dl class="facts">
      {#if revision.revisions}
        <dt>Position</dt>
        <dd data-nofilter>{revision.current ? 'Current: ' : ''}{formatCount(revision.position)} of {#if historyHref}<a href={historyHref}>{countOf(revision.revisions, 'revision')}</a>{:else}{countOf(revision.revisions, 'revision')}{/if}, newest first</dd>
      {/if}
      <dt>Previous</dt><dd class="mono">{#if revision.previous}<bdi>{label(revision.previous)}</bdi>{:else}none{/if}</dd>
      <dt>Next</dt><dd class="mono">{#if revision.next}<bdi>{label(revision.next)}</bdi>{:else}none{/if}</dd>
      <dt>Targets</dt>
      <dd data-nofilter>{formatCount(running)} of the service's {#if targetsHref}<a href={targetsHref}>{formatCount(revision.serviceTargets ?? 0)}</a>{:else}{formatCount(revision.serviceTargets ?? 0)}{/if} run this revision</dd>
    </dl>
  {/snippet}
  {#snippet caption()}{s.summary} {countOf(running, 'target')} of the service's {formatCount(revision.serviceTargets ?? 0)} run this revision.{' '}<span class="fig-ref">Dashed layers are the neighbours; the accent layer is this revision.</span>{/snippet}
</Figure>

{#if ev.total}
  <Figure id="evidence" testid="revision-evidence-figure" narrow question="Where does the evidence about it come from?"
    alt={`${countOf(ev.total, 'source')} report about ${version}: ${formatCount(evc.available ?? 0)} current, ${formatCount((evc.stale ?? 0) + (evc.partial ?? 0))} stale or partial, ${formatCount(evc.unavailable ?? 0)} unavailable.`} wires={ev.wires}>
    {#snippet stage()}
      <div class="evfig">
        <ul class="evfig-s" aria-label="Sources">
          {#each ev.marks as mk (mk.ref.key)}
            <li>
              <span class="evfig-t">
                <a class="id" href={fleetEntityUrl('source', mk.ref.key ?? '')} title={mk.ref.label}><bdi class:fit={elidable(mk.ref.label ?? '')}>{mk.ref.label}</bdi></a>
                <span class="t-meta">{mk.ref.secondary ? `${mk.ref.secondary} · ` : ''}{mk.words}</span>
              </span>
              <Node shape="square" tone={mk.tone} hatch={mk.hatch} glyph={mk.glyph} a={mk.a} m={mk.a} />
            </li>
          {/each}
          {#if ev.more}
            <li>
              <span class="evfig-t"><span data-nofilter>{formatCount(ev.more)} more {ev.more === 1 ? 'source' : 'sources'}</span><span class="t-meta">{ev.restCurrent ? 'all current' : 'not all current'}</span></span>
              <span class="cnt mk" data-a="em" aria-hidden="true">+{formatCount(ev.more)}</span>
            </li>
          {/if}
        </ul>
        <div class="evfig-h"><HubNode kind="revision" a="rv" /><span class="evfig-hl"><b class="id mono" title={version}><bdi class:fit={elidable(version)}>{version}</bdi></b><span class="t-meta">this revision</span></span></div>
      </div>
    {/snippet}
    {#snippet aside()}
      <dl class="facts" data-nofilter>
        <dt>Sources</dt><dd>{formatCount(ev.total)}</dd>
        <dt>Current</dt><dd>{formatCount(evc.available ?? 0)}</dd>
        <dt>Stale or partial</dt><dd>{formatCount((evc.stale ?? 0) + (evc.partial ?? 0))}</dd>
        <dt>Unavailable</dt><dd>{formatCount(evc.unavailable ?? 0)}</dd>
        {#if evOther}<dt>State unknown</dt><dd>{formatCount(evOther)}</dd>{/if}
      </dl>
    {/snippet}
    {#snippet caption()}{countOf(ev.total, 'source')} {ev.total === 1 ? 'reports' : 'report'} this revision or a target that runs it.{evc.unavailable ? ` ${countOf(evc.unavailable, 'source')} ${evc.unavailable === 1 ? 'is' : 'are'} unavailable, so what ${evc.unavailable === 1 ? 'it' : 'they'} would report is unknown.` : ''}{' '}<span class="fig-ref">Solid wire: reported. Dotted: stale or partial. Broken: unavailable.</span>{/snippet}
  </Figure>
{/if}
