<script>
  // The Owners and Domains tables (SPEC T3 r5): one row per owner or domain with its
  // services counted by compliance (non-compliant, unknown), health (failing), evidence
  // (stale) and knowledge (gaps), and the non-compliant rate. The count headers are
  // links that ask the server for that order (it sorts before it pages), and the active
  // one carries aria-sort; the default is the rank figure's ranking. No one column is
  // the health ranking, so one link beside the table asks for it. An owner's counts are
  // of the services it owns outright, as the figure counts them, so its Rate divides by
  // those (`owned`) and its links list exactly those services.
  import EntityLink from './EntityLink.svelte';
  import { formatCount } from '../lib/format.ts';
  import { sharePct } from '../lib/figure.ts';
  import { flip } from 'svelte/animate';
  import { MOTION, dur } from '../lib/motion.ts';

  /** @type {{ kind: 'owner' | 'domain', entities: any[], order: string, sortHref: (sort: string) => string, statusHref: (ref: any, status: string) => string, testid: string }} */
  let { kind, entities, order, sortHref, statusHref, testid } = $props();
  const owner = $derived(kind === 'owner');
  const base = (r) => (owner ? r.owned ?? 0 : r.services ?? 0);
  // Every count column has a floor of its words plus the cell padding, so none is
  // squeezed beside the others; below the floors' sum the table turns into cards
  // (components.css, dtable-xl).
  const NUM = 'max(calc(9ch + 1.5rem), 9%)';
  /** Name sorts A to Z; every count sorts most first. `status` links the count to the services it counts. */
  const cols = $derived([
    { key: 'name', label: owner ? 'Owner' : 'Domain', dir: 'ascending', w: 'max(10rem, 16%)' },
    { key: 'services', label: 'Services', dir: 'descending', field: 'services' },
    ...(owner ? [] : [{ key: '', label: 'Targets', dir: '', field: 'targets' }]),
    { key: 'nonCompliant', label: 'Non-compliant', dir: 'descending', field: 'nonCompliant', status: 'NonCompliant', w: 'max(calc(13ch + 1.5rem), 11%)' },
    { key: '', label: 'Unknown', dir: '', field: 'unknown', status: 'Unknown' },
    { key: '', label: 'Failing', dir: '', field: 'failingHealth' },
    { key: '', label: 'Stale', dir: '', field: 'stale' },
    { key: 'gaps', label: 'Gaps', dir: 'descending', field: 'withGaps' },
    { key: 'rate', label: 'Rate', dir: 'descending' },
  ]);
  const ranked = $derived(order === 'health');
</script>

<div class="gt-wrap">
  <a class="gt-rank" href={sortHref('health')} aria-current={ranked ? 'true' : undefined} data-testid="{testid}-rank-health">Rank by health</a>
  <div class="data-scroll cq-cards cq-cards-xl">
    <table class="data-table gt" data-testid={testid}>
      <caption class="visually-hidden">{owner ? 'Owners' : 'Domains'} and the health of their services</caption>
      <colgroup>{#each cols as c (c.label)}<col style:width={c.w ?? NUM} />{/each}</colgroup>
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
            {#each cols.slice(1) as c (c.label)}
              {#if c.field}
                {@const v = ref[c.field] ?? 0}
                <td class="num" data-label={c.label}>{#if c.status && v}<a href={statusHref(ref, c.status)}>{formatCount(v)}</a>{:else}{formatCount(v)}{/if}</td>
              {:else}
                <td class="num" data-label="Rate" title={owner ? `${formatCount(nc)} of the ${formatCount(base(ref))} services they own outright` : `${formatCount(nc)} of ${formatCount(base(ref))} services`}>{sharePct(nc, base(ref))}</td>
              {/if}
            {/each}
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<style>
  .gt-wrap { display: flex; flex-direction: column; gap: var(--sp-2); min-width: 0; }
  .gt-rank { align-self: flex-end; display: inline-flex; align-items: center; min-height: 2rem; color: var(--c-accent); font-size: var(--text-sm); }
  .gt-rank[aria-current='true'] { font-weight: 600; text-decoration: none; color: var(--c-text); }
  @media (pointer: coarse) { .gt-rank { min-height: 44px; } }
  .gt .num { text-align: right; font-variant-numeric: tabular-nums; }
  .gt-sort { color: inherit; text-decoration: none; }
  .gt-sort:hover, .gt-sort:focus-visible { text-decoration: underline; }
  @container dtable-xl (max-width: calc(80ch + 23rem)) { .gt .num { text-align: left; } }
</style>
