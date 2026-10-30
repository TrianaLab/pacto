<script>
  import ShowAll from '../components/ShowAll.svelte';
  import EnvironmentList from '../components/EnvironmentList.svelte';
  import { cap } from '../lib/cap.svelte.ts';
  import { formatCount } from '../lib/format.ts';
  import { joinAnd } from '../lib/answers.ts';
  import CollapsibleSection from '../CollapsibleSection.svelte';
  import SectionState from './SectionState.svelte';

  // Each declared environment and the check of its values file against the
  // configuration schemas, rendered by the same list the revision page uses.
  let { environments = [], open = $bindable(false), id = '' } = $props();

  const count = (status) => environments.filter((e) => e.status === status).length;
  // The closed section still says what needs attention: a failure, or a value set
  // Pacto could not check. Neither of the last two is a pass.
  const summary = $derived(joinAnd([['fail', 'failed'], ['unverified', 'not verified'], ['skip', 'not checked']]
    .map(([st, word]) => (count(st) ? `${formatCount(count(st))} ${word}` : '')).filter(Boolean), 'and')
    || 'Every environment passed');
  const rows = cap(25);
</script>

{#if environments?.length > 0}
  <CollapsibleSection title="Environments" count={environments.length} bind:open {id}>
    <p class="env-summary" class:env-failed={count('fail') > 0} data-testid="environments-summary">{summary}</p>
    <EnvironmentList items={rows.slice(environments)} />
    <ShowAll cap={rows} total={environments.length} noun="environments" />
  </CollapsibleSection>
{:else}
  <SectionState title="Environments" bind:open {id} />
{/if}

<style>
  .env-summary { margin: 0 0 var(--sp-3); font-size: var(--text-sm); color: var(--c-text-2); }
  .env-failed { color: var(--c-err); font-weight: 600; }
</style>
