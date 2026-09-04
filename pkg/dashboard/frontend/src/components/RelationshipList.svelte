<script>
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import { provenanceLabel, provenanceIsImplied } from '../lib/entityLabels.ts';
  import { corroborationLabel, corroborationTone, differenceLabel, differenceTone } from '../lib/graphState.ts';
  import { shortDigest } from '../lib/format.ts';

  // Renders the observed/differences relationship summary (NeighborhoodEdge items):
  // the reconciliation of each EXPECTED (declared) dependency against OBSERVED runtime
  // traffic, read verbatim from the backend edge (ADR-3). `direction` picks which end
  // to link ('to' for a dependency, 'from' for a dependent).
  //
  // `selfKey` is the entity whose page this is. A service's relationships include edges
  // in BOTH directions, so linking a fixed end made an inbound edge (frontend depends on
  // api-gateway) render as "api-gateway" on the api-gateway page -- the service listed
  // as its own relationship, and two edges with the same counterpart became two
  // identical-looking rows. Naming the OTHER end, and which way the dependency runs,
  // makes each row a sentence about somebody else.
  //
  // `showClaims` turns on the declared detail (ref / required / compatibility / pin).
  // It is off by default because the service and target pages use this list as a
  // neighbourhood SUMMARY; the revision page is the contract inspector, and there the
  // declaration itself is the point.
  let { items = [], direction = 'to', selfKey = '', showClaims = false } = $props();

  // Which way this row's dependency runs. With a `selfKey` it is decided per edge (a
  // service's neighbourhood mixes both directions); without one the caller has already
  // scoped the list, so `direction` decides. An edge missing its `from` end is treated
  // as outbound, so a partial payload still links a real entity instead of "(unknown)".
  const outbound = (e) => (selfKey ? !e.from || e.from.key === selfKey : direction === 'to');
  const counterpart = (e) => (outbound(e) ? e.to : e.from);
  const relationWord = (e) => {
    if (!selfKey) return '';
    if (e.relation === 'runs') return outbound(e) ? 'Runs' : 'Run by';
    return outbound(e) ? 'Depends on' : 'Used by';
  };

  // What a revision actually DECLARED about the edge -- the requested ref, whether the
  // dependency is required, the compatibility constraint and the lockfile pin. The
  // backend has always sent these (NeighborhoodEdge.declaredClaims); the row used to
  // drop them, which turned a dependency table into a list of names. They are shown
  // only on an outbound edge: an inbound row is somebody ELSE's declaration, and the
  // claim would read as this entity's own.
  const claims = (e) => (showClaims && outbound(e) ? (e.declaredClaims?.items ?? []) : []);
  // The claims preview is bounded like every other preview, so an edge declared by
  // more revisions than fit says so instead of implying the list is all of them.
  const claimsTruncated = (e) => showClaims && outbound(e) && !!e.declaredClaims?.truncated;

  // A FINE-GRAINED edge (a revision's or a target's dependency) carries no edge-scope
  // `difference`: observation is recorded per SERVICE, so the backend sends the
  // service-scoped verdict as observationScope + serviceCorroboration instead. Reading
  // the absent difference verbatim printed "Unknown" over relationships the fleet had
  // corroborated, so each scope is rendered in its own vocabulary. Same mapping the
  // graph drawer uses (lib/graphState.ts) -- never re-inferred from booleans.
  const scoped = (e) => !e.difference && !!e.serviceCorroboration;
  const verdictLabel = (e) => (scoped(e) ? corroborationLabel(e.serviceCorroboration) : differenceLabel(e.difference));
  const verdictTone = (e) => (scoped(e) ? corroborationTone(e.serviceCorroboration) : differenceTone(e.difference));

  const claimFacts = (c) => {
    const out = [];
    if (c.requestedRef) out.push(['Ref', c.requestedRef]);
    out.push(['Required', c.required ? 'Yes' : 'No']);
    if (c.compatibility) out.push(['Compatibility', c.compatibility]);
    const pin = [c.lockedVersion, c.lockedDigest ? `@${shortDigest(c.lockedDigest)}` : ''].filter(Boolean).join(' ');
    if (pin) out.push(['Pinned', pin]);
    return out;
  };
</script>

<ul class="rel-list">
  {#each items as e, i (e.id ?? i)}
    <li class="rel-item">
      <div class="rel">
        {#if selfKey}<span class="rel-word">{relationWord(e)}</span>{/if}
        <EntityLink ref={counterpart(e)} showStatus={false} showKind={!selfKey} />
        <IdentityBadge label={verdictLabel(e)} tone={verdictTone(e)} />
        {#if e.provenance && !provenanceIsImplied(e.difference, e.provenance)}<span class="rel-prov">{provenanceLabel(e.provenance)}</span>{/if}
        {#if e.stale}<span class="rel-stale">stale</span>{/if}
      </div>
      {#each claims(e) as c, ci (c.sourceRevision ?? ci)}
        <dl class="rel-claim">
          {#if claims(e).length > 1 && c.sourceRevision}
            <dt>Declared by</dt><dd><code>{shortDigest(c.sourceRevision.split('@').pop()) || c.sourceRevision}</code></dd>
          {/if}
          {#each claimFacts(c) as [label, value] (label)}
            <dt>{label}</dt><dd>{value}</dd>
          {/each}
        </dl>
      {/each}
      {#if claimsTruncated(e)}
        <p class="rel-more">More revisions declare this dependency than are shown here.</p>
      {/if}
    </li>
  {/each}
</ul>

<style>
  .rel-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .rel-item { display: flex; flex-direction: column; gap: var(--sp-1); }
  .rel { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .rel-word { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .rel-prov { font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.03em; }
  .rel-stale { font-size: var(--text-xs); color: var(--c-warn); }
  /* The declared claim reads as label/value pairs so the meaning of "Yes" or
     "^1.2.0" survives a screen reader and a 320px viewport, where a table would
     not. It wraps instead of scrolling. */
  .rel-claim {
    display: flex; flex-wrap: wrap; align-items: baseline; gap: 2px var(--sp-2);
    margin: 0 0 0 var(--sp-3); padding-left: var(--sp-3);
    border-left: 2px solid var(--c-border);
    font-size: var(--text-xs);
  }
  .rel-claim dt { color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.03em; }
  .rel-claim dd { margin: 0 var(--sp-2) 0 0; color: var(--c-text-2); word-break: break-all; }
  .rel-claim code { font-size: inherit; }
  .rel-more { margin: 0 0 0 var(--sp-3); font-size: var(--text-xs); color: var(--c-text-3); }
</style>
