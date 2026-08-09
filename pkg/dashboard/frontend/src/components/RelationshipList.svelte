<script>
  import EntityLink from './EntityLink.svelte';
  import IdentityBadge from './IdentityBadge.svelte';
  import { differenceLabel, differenceTone, provenanceLabel, provenanceIsImplied } from '../lib/entityLabels.ts';

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
  let { items = [], direction = 'to', selfKey = '' } = $props();

  // An edge missing its `from` end is treated as outbound, so a partial payload still
  // links a real entity instead of rendering "(unknown)".
  const outbound = (e) => !e.from || e.from.key === selfKey;
  const counterpart = (e) => {
    if (!selfKey) return direction === 'from' ? e.from : e.to;
    return outbound(e) ? e.to : e.from;
  };
  const relationWord = (e) => {
    if (!selfKey) return '';
    if (e.relation === 'runs') return outbound(e) ? 'Runs' : 'Run by';
    return outbound(e) ? 'Depends on' : 'Used by';
  };
</script>

<ul class="rel-list">
  {#each items as e, i (e.id ?? i)}
    <li class="rel">
      {#if selfKey}<span class="rel-word">{relationWord(e)}</span>{/if}
      <EntityLink ref={counterpart(e)} showStatus={false} showKind={!selfKey} />
      <IdentityBadge label={differenceLabel(e.difference)} tone={differenceTone(e.difference)} />
      {#if e.provenance && !provenanceIsImplied(e.difference, e.provenance)}<span class="rel-prov">{provenanceLabel(e.provenance)}</span>{/if}
      {#if e.stale}<span class="rel-stale">stale</span>{/if}
    </li>
  {/each}
</ul>

<style>
  .rel-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .rel { display: flex; align-items: center; gap: var(--sp-2); flex-wrap: wrap; }
  .rel-word { font-size: var(--text-xs); text-transform: uppercase; letter-spacing: 0.04em; color: var(--c-text-3); }
  .rel-prov { font-size: var(--text-xs); color: var(--c-text-3); text-transform: uppercase; letter-spacing: 0.03em; }
  .rel-stale { font-size: var(--text-xs); color: var(--c-warn); }
</style>
