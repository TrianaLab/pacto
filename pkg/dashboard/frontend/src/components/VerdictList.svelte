<script>
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import DigestText from './DigestText.svelte';
  import { miraLink, verdictBadge, unknownHelp, gateLabel, UNKNOWN_DOCS, timeLabel } from '../lib/delivery.ts';
  import { fleetEntityUrl } from '../lib/router.ts';

  // Verdicts with the proof behind each: the citation, a link into Mira for a
  // health check when Mira's address is known (else the query to copy) and the
  // command that runs the check again. The proof is for auditors, so it starts
  // folded; an Unknown says what Pacto could not see and how to fix that.
  let { verdicts = [], miraURL = '', testid = 'verdicts' } = $props();

  // Keys shown elsewhere in the row, or as their own control. The runner's
  // signed answer is for auditors to check (pacto delivery record verify),
  // not to read here.
  const SHOWN = new Set(['query', 'errorQuery', 'runner', 'runnerBundle']);

  // A contract verdict cites each Pacto it judged as <pacto>.target, .contractStatus,
  // .evaluatedAt and .findings: listed per Pacto, linked to its target, never in the proof twice.
  const PACTO_KEYS = ['target', 'contractStatus', 'evaluatedAt', 'findings'];
  function pactosOf(v) {
    if (v.gate !== 'pacto.contract' || !v.citation) return [];
    const c = v.citation;
    return Object.keys(c).filter((k) => k.endsWith('.target')).map((k) => k.slice(0, -'.target'.length)).sort()
      .map((n) => ({ name: n, target: c[`${n}.target`], status: c[`${n}.contractStatus`] ?? '', at: c[`${n}.evaluatedAt`] ?? '', findings: c[`${n}.findings`] ?? '' }));
  }
  const proofOf = (v, pactos) => Object.entries(v.citation ?? {}).filter(([k]) => !SHOWN.has(k) && !pactos.some((p) => PACTO_KEYS.some((s) => k === `${p.name}.${s}`)));
</script>

{#if verdicts.length === 0}
  <p class="t-body-2 vl-none">No checks yet.</p>
{:else}
  <ul class="vl-list" data-testid={testid}>
    {#each verdicts as v, i (v.gate + v.phase + i)}
      {@const b = verdictBadge(v)}
      {@const pactos = pactosOf(v)}
      <li class="vl-item">
        <div class="vl-head">
          <span class="badge {b.tone}" data-testid="verdict-badge"><span class="badge-dot"></span>{b.label}</span>
          {#if b.chip}<span class="badge badge-warn" data-testid="verdict-chip">{b.chip}</span>{/if}
          <strong>{gateLabel(v.gate)}</strong>{#if gateLabel(v.gate) !== v.gate}<code class="t-meta vl-id">{v.gate}</code>{/if}
          <span class="t-meta">{v.phase === 'pre' ? 'before the change' : v.phase === 'verify' ? 'after the change' : v.phase}</span>
        </div>
        {#if v.message}<p class="vl-msg t-body-2">{v.message}</p>{/if}
        {#if pactos.length}
          <ul class="vl-pactos t-body-2" data-testid="contract-pactos" aria-label="Pactos it judged">
            {#each pactos as p (p.name)}
              <li><a href={fleetEntityUrl('target', p.target)}><bdi>{p.name}</bdi></a>{#if p.status}{' '}<span>{p.status}</span>{/if}{#if p.at}{' '}<span class="t-meta">evaluated <time datetime={p.at}>{timeLabel(p.at)}</time></span>{/if}{#if p.findings}{' '}<span>findings <bdi>{p.findings}</bdi></span>{/if}</li>
            {/each}
          </ul>
        {/if}
        {#if v.citation?.runner}
          <p class="vl-msg t-body-2" data-testid="verdict-runner">Run and signed by <strong>{v.citation.runner}</strong>{#if v.citation.runnerBundle}, whose signed answer the record keeps{/if}.</p>
        {/if}
        {#if v.result === 'Unknown'}
          <p class="vl-unknown t-body-2" data-testid="verdict-unknown">{unknownHelp(v)} Until it can, a person accepts the unknown or rejects the change. <a href={UNKNOWN_DOCS} target="_blank" rel="noopener noreferrer">What Unknown means</a></p>
        {/if}
        {#if (v.citation && Object.keys(v.citation).length) || v.rerun}
          <details class="vl-more disclosure">
            <summary><span class="disclosure-caret" data-motion aria-hidden="true"></span>Proof</summary>
            {#if v.citation && Object.keys(v.citation).length}
              <dl class="vl-cite t-body-2">
                {#each proofOf(v, pactos) as [k, val] (k)}
                  <dt>{k}</dt><dd><code><DigestText text={val} /></code></dd>
                {/each}
              </dl>
              {#if v.citation.query}
                {@const href = miraLink(v.citation, miraURL)}
                <p class="vl-proof t-body-2">
                  {#if href}
                    <a {href} target="_blank" rel="noopener noreferrer" data-testid="mira-link">Open the spans in Mira</a>
                    {#if v.citation.from}<span class="t-meta">(window {v.citation.from} to {v.citation.to})</span>{/if}
                  {:else}
                    <span>Mira query:</span> <CopyableIdentifier value={v.citation.query} label="Mira query" />
                  {/if}
                </p>
              {/if}
            {/if}
            {#if v.rerun}
              <p class="vl-proof t-body-2"><span>Run it again:</span> <CopyableIdentifier value={v.rerun} label="command" /></p>
            {/if}
          </details>
        {/if}
      </li>
    {/each}
  </ul>
{/if}

<style>
  .vl-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .vl-item { background: var(--c-surface); border: 1px solid var(--c-border); border-radius: var(--radius-sm); padding: var(--sp-3); display: flex; flex-direction: column; gap: var(--sp-1); }
  .vl-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; }
  .vl-msg, .vl-proof { margin: 0; }
  .vl-pactos { margin: 0; padding-left: var(--sp-5); overflow-wrap: anywhere; }
  .vl-pactos a { color: var(--c-accent); text-decoration: underline; }
  .vl-proof a { color: var(--c-accent); text-decoration: underline; }
  .vl-cite { display: grid; grid-template-columns: max-content 1fr; gap: 2px var(--sp-3); margin: 0; }
  .vl-cite dt { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .vl-cite dd { margin: 0; overflow-wrap: anywhere; }
  .vl-none { color: var(--c-text-3); }
  .vl-id { color: var(--c-text-3); }
  .vl-unknown { margin: 0; }
  .vl-unknown a { color: var(--c-accent); text-decoration: underline; }
</style>
