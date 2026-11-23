<script>
  import { api, ApiError } from '../lib/api.ts';
  import { candidateGroups, candidateWord, candidateTip, candidateLabels } from '../lib/clusters.ts';
  import { saveFile } from '../lib/saveFile.ts';
  import { formatDate } from '../lib/dateFormat.ts';
  import ProvenanceMark from './ProvenanceMark.svelte';

  // The candidate clusters (514-S5) inside Data sources' Clusters block: what the hub's
  // GitOps and cluster managers know, grouped by source, as small cards. A candidate
  // matches a member only by a UID it states, so the one negative word is "not matched";
  // the UID shows in the source mark's tip and in no label. Argo CD and Flux groups hand
  // out the generated install, fetched through the API client and saved from a blob.
  // `members` are the uids of the member chips above, which a matched card opens.
  let { candidates = [], asOf = '', completeness = '', members = new Set(), memberAnchor = (uid) => uid } = $props();

  const groups = $derived(candidateGroups(candidates));
  const partial = $derived(!!completeness && completeness !== 'complete');
  // One error per engine, shown under the button that failed; busy is the engine whose
  // download runs, so a second tap waits for the first.
  let errors = $state({});
  let busy = $state('');

  const nameOf = (c) => c.displayName || c.name;

  async function download(from) {
    if (busy) return;
    busy = from;
    errors = { ...errors, [from]: '' };
    try {
      saveFile(`pacto-members-${from}.yaml`, await api.fleetClusterInstall(from));
    } catch (e) {
      errors = { ...errors, [from]: e instanceof ApiError && e.status === 403
        ? 'Only those who may approve clusterjoins can download the member install.'
        : e?.message || 'Pacto could not generate the member install.' };
    } finally {
      busy = '';
    }
  }

  function openMember(uid) {
    const el = document.getElementById(memberAnchor(uid));
    el?.scrollIntoView({ block: 'center' });
    el?.focus();
  }
</script>

<details class="disclosure scc sc-candidates" data-testid="cluster-candidates">
  <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>{candidates.length} candidate cluster{candidates.length === 1 ? '' : 's'}{partial ? ', partial' : ''}</summary>
  <p class="t-meta scc-note">The clusters the hub's GitOps and cluster managers know{asOf ? `, as of ${formatDate(asOf)}` : ''}. Nothing joins on its own: each new cluster waits for a person's approval.</p>
  {#each groups as g (g.source)}
    <section class="scc-group" data-testid="cluster-candidate-group" data-source={g.source} aria-label={`Candidate clusters from ${g.label}`}>
      <p class="scc-head">
        <span class="scc-from">From {g.label}</span>
        {#if g.install}<button type="button" class="btn btn-sm scc-action" data-testid="cluster-candidate-install" disabled={busy === g.install} aria-busy={busy === g.install} onclick={() => download(g.install)}>{g.action}</button>{/if}
      </p>
      {#if g.install && errors[g.install]}<p class="scc-error" role="alert" data-testid="cluster-candidate-error">{errors[g.install]}</p>{/if}
      <ul class="scc-cards">
        <!-- A Flux kubeconfig named with two keys gives two candidates of one name, so the index keys too. -->
        {#each g.items as c, i (`${c.kind}/${c.namespace ?? ''}/${c.name}/${i}`)}
          {@const l = candidateLabels(c.labels)}
          <li class="scc-card" class:scc-muted={c.match !== 'matched'} data-testid="cluster-candidate" data-match={c.match}>
            {#if c.match === 'matched' && c.member && members.has(c.member.uid)}
              <button type="button" class="scc-name scc-open" data-testid="cluster-candidate-member" onclick={() => openMember(c.member.uid)}><bdi title={nameOf(c)}>{nameOf(c)}</bdi></button>
            {:else}
              <bdi class="scc-name" title={nameOf(c)}>{nameOf(c)}</bdi>
            {/if}
            {#if l.shown.length}<span class="scc-labels" title={l.title}>{l.shown.join(' ')}</span>{/if}
            {#if l.more}<span class="scc-more" title={l.title}>+{l.more}</span>{/if}
            {#if c.match === 'matched' && c.member?.name}
              <span class="scc-word">matched to</span><bdi class="scc-member" title={c.member.name}>{c.member.name}</bdi>
            {:else}
              <span class="scc-word">{candidateWord(c)}</span>
            {/if}
            <ProvenanceMark label={`Where ${nameOf(c)} came from`} text={candidateTip(c)} />
          </li>
        {/each}
      </ul>
    </section>
  {/each}
</details>

<style>
  .scc > :not(summary) { margin-top: var(--sp-2); }
  .scc-note, .scc-head { margin: 0; }
  .scc-head { display: flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); min-inline-size: 0; font-size: var(--text-sm); }
  .scc-from { font-weight: 600; }
  .scc-cards { list-style: none; margin: var(--sp-1) 0 0; padding: 0; display: flex; flex-wrap: wrap; gap: var(--sp-2); min-inline-size: 0; }
  .scc-card { display: inline-flex; flex-wrap: wrap; align-items: center; gap: var(--sp-2); max-inline-size: 100%; min-inline-size: 0; padding: 2px var(--sp-2) 2px var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); font-size: var(--text-sm); }
  /* Not matched or only the same UID: inferred, so muted. */
  .scc-muted { border-style: dashed; color: var(--c-text-2); }
  /* A 63-character name or a long label run ends in an ellipsis; the title holds it whole. */
  .scc-name, .scc-name bdi { min-inline-size: 0; max-inline-size: 28ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .scc-open { display: inline-flex; padding: 0; border: 0; background: none; color: var(--c-accent); font: inherit; font-weight: 600; text-decoration: underline; cursor: pointer; }
  .scc-labels { min-inline-size: 0; max-inline-size: 36ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-family: var(--font-mono); font-size: var(--text-xs); color: var(--c-text-3); }
  .scc-more { flex: none; font-size: var(--text-xs); color: var(--c-text-3); }
  /* The member name of a matched card is a 63-character label at most: an ellipsis, whole in its title. */
  .scc-word { flex: none; color: var(--c-text-2); }
  .scc-member { min-inline-size: 0; max-inline-size: 28ch; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--c-text-2); }
  .scc-error { margin: 0; color: var(--c-warn); overflow-wrap: anywhere; }
  @media (pointer: coarse) { .scc-open, .scc-action { min-block-size: 44px; } }
</style>
