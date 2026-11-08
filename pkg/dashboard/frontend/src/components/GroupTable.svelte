<script>
  // The Owners and Domains tables (SPEC T3 r5): one row per owner or domain with its
  // services, its non-compliant services and their rate. The count headers are links
  // that ask the server for that order (it sorts before it pages), and the active one
  // carries aria-sort; the default is the rank figure's ranking. An owner's
  // non-compliant count is of the services it owns outright, as the figure counts it,
  // so its Rate divides by those (`owned`) and its link lists exactly those services.
  import EntityLink from './EntityLink.svelte';
  import { formatCount } from '../lib/format.ts';
  import { sharePct } from '../lib/figure.ts';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  /** @type {{ kind: 'owner' | 'domain', entities: any[], order: string, sortHref: (sort: string) => string, ncHref: (ref: any) => string, testid: string }} */
  let { kind, entities, order, sortHref, ncHref, testid } = $props();
  const owner = $derived(kind === 'owner');
  const base = (r) => (owner ? r.owned ?? 0 : r.services ?? 0);
  /** Name sorts A to Z; every count sorts most first. */
  const cols = $derived([
    { key: 'name', label: owner ? 'Owner' : 'Domain', dir: 'ascending' },
    { key: 'services', label: 'Services', dir: 'descending', w: '8rem' },
    ...(owner ? [] : [{ key: '', label: 'Targets', dir: '', w: '8rem' }]),
    { key: 'nonCompliant', label: 'Non-compliant', dir: 'descending', w: '10rem' },
    { key: 'rate', label: 'Rate', dir: 'descending', w: '6.5rem' },
  ]);
</script>

<div class="data-scroll">
  <table class="data-table gt" data-testid={testid}>
    <caption class="visually-hidden">{owner ? 'Owners' : 'Domains'} and their non-compliant services</caption>
    <colgroup>{#each cols as c (c.label)}<col style:width={c.w} />{/each}</colgroup>
    <thead>
      <tr>
        {#each cols as c (c.label)}
          <th scope="col" class:num={c.key !== 'name'} aria-sort={c.key ? (order === c.key ? c.dir : 'none') : undefined}>
            {#if c.key}<a class="gt-sort" href={sortHref(c.key)}>{c.label}{#if order === c.key}<span aria-hidden="true">{c.dir === 'ascending' ? ' ▲' : ' ▼'}</span>{/if}</a>{:else}{c.label}{/if}
          </th>
        {/each}
      </tr>
    </thead>
    <tbody>
      {#each entities as ref (ref.key)}
        {@const nc = ref.nonCompliant ?? 0}
        <tr animate:flip={{ duration: dur(MOTION.row) }}>
          <th scope="row"><EntityLink {ref} showStatus={false} showKind={false} /></th>
          <td class="num" data-label="Services">{formatCount(ref.services ?? 0)}</td>
          {#if !owner}<td class="num" data-label="Targets">{formatCount(ref.targets ?? 0)}</td>{/if}
          <td class="num" data-label="Non-compliant">{#if nc}<a href={ncHref(ref)}>{formatCount(nc)}</a>{:else}0{/if}</td>
          <td class="num" data-label="Rate" title={owner ? `${formatCount(nc)} of the ${formatCount(base(ref))} services they own outright` : `${formatCount(nc)} of ${formatCount(base(ref))} services`}>{sharePct(nc, base(ref))}</td>
        </tr>
      {/each}
    </tbody>
  </table>
</div>

<style>
  .gt .num { text-align: right; font-variant-numeric: tabular-nums; }
  .gt-sort { color: inherit; text-decoration: none; }
  .gt-sort:hover, .gt-sort:focus-visible { text-decoration: underline; }
  @media (max-width: 768px) { .gt .num { text-align: left; } }
</style>
