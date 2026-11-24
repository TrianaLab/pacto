<script>
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import ProvenanceMark from './ProvenanceMark.svelte';
  import { fleetServicesUrl } from '../lib/router.ts';
  import { shortDigest, timeLabel, sentence, memberStateLabel, placementSourceLabel } from '../lib/delivery.ts';
  import { countOf } from '../lib/format.ts';

  // The Members block on the Environments board of one service (514-S6): per
  // environment members host, in the order the server gives (the service's promotion
  // graph), the members a promote is placed on, where that placement came from and what
  // each member answered to the newest request. Each member is a mark (its name: opens
  // the services that run on it) and a sibling glyph (its state: opens the reason and
  // the next command). Collapsed unless a member needs a look. The dashboard never
  // signs: Confirm placement hands a person the command that signs the requests.
  let { plans = [], service = '' } = $props();
  const id = $props.id();

  const GLYPHS = { applied: '✓', held: '⏸', refused: '✕', rejected: '✕', failed: '!', waiting: '…', unknown: '?', raised: '↑', 'no request': '–' };
  const muted = (m) => !!m.stale || m.state === 'unknown';
  const looks = (m) => muted(m) || m.state === 'held' || m.state === 'refused';
  const attention = $derived(plans.flatMap((p) => p.members ?? []).filter(looks).length);
  const memberCount = $derived(new Set(plans.flatMap((p) => (p.members ?? []).map((m) => m.uid))).size);

  // A glyph toggles its member's detail; a held or refused member's starts open.
  let toggled = $state({});
  const key = (p, m) => `${p.environment}\u0000${m.uid}`;
  const shown = (p, m) => toggled[key(p, m)] ?? (m.state === 'held' || m.state === 'refused');
  const flip = (p, m) => { toggled = { ...toggled, [key(p, m)]: !shown(p, m) }; };
  let confirming = $state('');

  const sourceTip = (p) => [
    `${placementSourceLabel(p.placement)}${p.placement.policy ? ` ${p.placement.policy}` : ''}.`,
    ...(p.placement.clusters ?? []).map((c) => `${c.name} (${c.uid}): ${c.evidence}.`),
  ].join(' ');
</script>

<details class="disclosure mp member-placement" data-testid="member-placement" open={attention > 0}>
  <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>
    <span class="mp-title">Members: {countOf(memberCount, 'member')} in {countOf(plans.length, 'environment')}{attention ? `, ${attention} ${attention === 1 ? 'needs' : 'need'} a look` : ''}</span>
  </summary>
  <ul class="mp-envs">
    {#each plans as p (p.environment)}
      <li class="mp-env" data-testid="member-env" data-environment={p.environment}>
        <p class="mp-head">
          <bdi class="mp-env-name" title={p.environment}>{p.environment}</bdi>
          <span data-testid="member-placement-source"><ProvenanceMark label={`Where ${service} runs in ${p.environment} across members`} text={sourceTip(p)} /></span>
        </p>
        <ul class="mp-members">
          {#each p.members ?? [] as m (m.uid)}
            {@const label = memberStateLabel(m.state)}
            <!-- The uid and full digest are the member's tooltip, on the row: a title on the
                 mark alone would claim to be the name it shows. -->
            <li class="mp-member" class:mp-muted={muted(m)} data-testid="member" data-state={m.state} title={`${m.name} (${m.uid})${m.digest ? `, ${m.digest}` : ''}`}>
              <a class="mp-mark" href={fleetServicesUrl({ cluster: m.uid })}>
                <bdi>{m.name}</bdi>{#if m.digest}<span class="mp-digest">{shortDigest(m.digest)}</span>{/if}
              </a>
              <button type="button" class="mp-glyph tone-{m.state.replace(' ', '-')}" aria-label={`${m.name}: ${label}`} title={label}
                aria-expanded={shown(p, m)} aria-controls={`${id}-${p.environment}-${m.uid}`} onclick={() => flip(p, m)}><span aria-hidden="true">{GLYPHS[m.state] ?? '•'}</span></button>
              {#if muted(m) && m.asOf}<span class="t-meta">as of {timeLabel(m.asOf)}</span>{/if}
            </li>
          {/each}
        </ul>
        {#each (p.members ?? []).filter((m) => shown(p, m)) as m (m.uid)}
          <div class="mp-detail" id={`${id}-${p.environment}-${m.uid}`} data-testid="member-detail">
            <p class="mp-text"><bdi>{m.name}</bdi>: {memberStateLabel(m.state)}{m.reason ? `: ${m.reason.trim().replace(/\.$/, '')}.` : '.'}</p>
            {#if m.next}<CopyableIdentifier value={m.next} label="command" />{/if}
          </div>
        {/each}
        {#if p.placement.unplaced?.length}
          <p class="mp-text mp-muted-text">Not placed: {#each p.placement.unplaced as u, i (u.uid)}{i ? '; ' : ''}<span title={`${u.name} (${u.uid})`}><bdi>{u.name}</bdi>: {u.reason}</span>{/each}.</p>
        {/if}
        {#if p.hidden}<p class="mp-text mp-muted-text">{countOf(p.hidden, 'member')} hidden{p.hiddenReason ? `: ${p.hiddenReason}` : ''}.</p>{/if}
        {#if p.completeness === 'partial'}<p class="mp-text mp-muted-text">Read in part{p.notes?.length ? `: ${p.notes.join('; ')}` : ''}.</p>{/if}
        {#if p.next}
          <button type="button" class="btn mp-btn" aria-expanded={confirming === p.environment} aria-controls={`${id}-${p.environment}-confirm`}
            onclick={() => (confirming = confirming === p.environment ? '' : p.environment)}>Confirm placement</button>
          {#if confirming === p.environment}
            <div class="mp-panel" id={`${id}-${p.environment}-confirm`}>
              <CopyableIdentifier value={p.next} label="command" />
              <p class="mp-text">Confirming signs one request per member as you. Each member checks it with its own trust and RBAC, then treats it as a proposal its own gates decide.</p>
            </div>
          {/if}
        {/if}
      </li>
    {/each}
  </ul>
</details>

<style>
  .mp summary { display: flex; align-items: center; gap: var(--sp-2); cursor: pointer; min-block-size: var(--touch-min); }
  .mp-title { font-size: var(--text-sm); font-weight: 600; overflow-wrap: anywhere; min-inline-size: 0; }
  .mp-envs { display: flex; flex-direction: column; gap: var(--sp-3); margin: var(--sp-2) 0 0; padding: 0; list-style: none; }
  .mp-env { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; }
  .mp-head { display: flex; align-items: center; gap: var(--sp-2); margin: 0; font-size: var(--text-sm); }
  .mp-env-name { font-weight: 500; overflow-wrap: anywhere; min-inline-size: 0; }
  .mp-members { display: flex; flex-wrap: wrap; gap: var(--sp-2) var(--sp-3); margin: 0; padding: 0; list-style: none; }
  .mp-member { display: inline-flex; align-items: center; gap: var(--sp-1); min-inline-size: 0; font-size: var(--text-sm); }
  .mp-mark { display: inline-flex; flex-wrap: wrap; align-items: baseline; column-gap: var(--sp-2); min-inline-size: 0; overflow-wrap: anywhere; }
  .mp-digest { font-family: var(--font-mono); font-size: var(--text-xs); white-space: nowrap; }
  .mp-glyph { display: inline-flex; align-items: center; justify-content: center; min-inline-size: 1.75rem; min-block-size: 1.75rem; padding: 0; border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); color: var(--c-text); cursor: pointer; }
  .mp-glyph.tone-applied { color: var(--c-ok); }
  .mp-glyph.tone-held, .mp-glyph.tone-waiting { color: var(--c-warn); }
  .mp-glyph.tone-refused, .mp-glyph.tone-rejected, .mp-glyph.tone-failed { color: var(--c-err); }
  .mp-muted, .mp-muted .mp-mark, .mp-muted .mp-glyph, .mp-muted-text { color: var(--c-text-3); }
  .mp-detail, .mp-panel { display: flex; flex-direction: column; gap: var(--sp-2); min-inline-size: 0; padding: var(--sp-2) var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface); }
  .mp-text { margin: 0; font-size: var(--text-sm); overflow-wrap: anywhere; }
  .mp-btn { align-self: flex-start; min-block-size: var(--touch-min); }
  @media (pointer: coarse) {
    .mp summary, .mp-btn { min-block-size: 44px; }
    .mp-mark { display: inline-flex; align-items: center; min-block-size: 44px; min-inline-size: 44px; }
    .mp-glyph { min-block-size: 44px; min-inline-size: 44px; }
    .mp :global(.copyable-btn) { min-block-size: 44px; min-inline-size: 44px; }
  }
</style>
