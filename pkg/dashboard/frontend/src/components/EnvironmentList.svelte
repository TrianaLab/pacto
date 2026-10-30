<script>
  import IdentityBadge from './IdentityBadge.svelte';
  import SeverityBadge from './SeverityBadge.svelte';
  import CopyableIdentifier from './CopyableIdentifier.svelte';
  import CappedList from './CappedList.svelte';
  import { formatCount } from '../lib/format.ts';
  import { environmentBadge, locationText } from '../lib/behavior.ts';

  // Each declared environment, the values file it names and what checking those values
  // against the configuration schemas found. Failed and Not verified are different
  // answers: a failure is a value the schema rejects, located in its file; Not verified
  // means Pacto could not check, and Not checked that the check never ran (the reason
  // says which earlier stage stopped it). Neither of the last two is a pass.
  let { items = [] } = $props();
  // Every message names its environment, which this card already does.
  const message = (env, f) => {
    const prefix = `environment ${env.name}: `;
    return f.message?.startsWith(prefix) ? f.message.slice(prefix.length) : f.message;
  };
</script>

<ul class="env-list">
  {#each items as env, i (`${env.name}:${i}`)}
    {@const badge = environmentBadge(env.status)}
    {@const findings = env.findings?.items ?? []}
    <li class="env" data-testid="environment">
      <div class="env-head">
        <span class="env-name" title={env.name}>{env.name}</span>
        <IdentityBadge label={badge.label} tone={badge.tone} />
        {#if env.file}<span class="env-file"><span class="env-k">File</span><CopyableIdentifier value={env.file} label="values file" /></span>{/if}
      </div>
      {#if env.status === 'skip' && env.reason}
        <p class="env-note">Not checked: {env.reason}.</p>
      {/if}
      {#if findings.length}
        <CappedList items={findings} max={5} label={`Findings for ${env.name}`}>
          {#snippet item(f)}
            <div class="env-f">
              <SeverityBadge severity={f.severity} />
              <div class="env-fbody">
                <span class="env-msg">{message(env, f)}</span>
                <span class="env-meta">{f.code}{#if locationText(f)}{' · '}<code>{locationText(f)}</code>{/if}</span>
              </div>
            </div>
          {/snippet}
        </CappedList>
        {#if env.findings?.truncated}
          <p class="env-note">Showing {formatCount(env.findings.count)} of {formatCount(env.findings.total)} findings.</p>
        {/if}
      {/if}
    </li>
  {/each}
</ul>

<style>
  .env-list { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: var(--sp-3); }
  .env { display: flex; flex-direction: column; gap: var(--sp-2); padding: var(--sp-3); border: 1px solid var(--c-border); border-radius: var(--radius-sm); background: var(--c-surface-inset); min-width: 0; }
  .env-head { display: flex; gap: var(--sp-2); align-items: center; flex-wrap: wrap; min-width: 0; }
  .env-name { font-weight: 600; color: var(--c-text); overflow-wrap: anywhere; }
  .env-file { display: inline-flex; gap: var(--sp-2); align-items: center; min-width: 0; max-width: 100%; }
  .env-k { color: var(--c-text-3); font: var(--role-label-font); font-variant-caps: var(--role-label-caps); letter-spacing: var(--role-label-tracking); }
  .env-note { margin: 0; font-size: var(--text-sm); color: var(--c-text-3); overflow-wrap: anywhere; }
  .env :global(.cl) { list-style: none; padding: 0; display: flex; flex-direction: column; gap: var(--sp-2); }
  .env-f { display: flex; gap: var(--sp-2); align-items: flex-start; min-width: 0; }
  .env-fbody { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
  .env-msg { font-size: var(--text-sm); color: var(--c-text); overflow-wrap: anywhere; min-width: 0; }
  .env-meta { font-size: var(--text-xs); color: var(--c-text-3); overflow-wrap: anywhere; }
  .env-meta code { font-size: inherit; }
</style>
