<script>
  // EnvMark: one Stage's state as the board's environment mark (ENV_STATES), with the
  // state word as its accessible name, or beside it as text when `word` is set. The
  // shape differs per state, so colour is never the only cue. `unknown` is the
  // status-only state (operations not read): hatched, and the board never passes it.
  // `decorative` hides it from assistive technology, for a control that names the state itself.
  import { envStateWord } from '../../lib/envFigure.ts';

  /** @type {{ state: string, href?: string, word?: boolean, decorative?: boolean }} */
  let { state, href = '', word = false, decorative = false } = $props();
  const label = $derived(envStateWord(state));
</script>

{#if decorative}
  <span class="env-mark" aria-hidden="true" data-testid="env-state-mark" data-state={state}><span class="ev-dot s-{state}"></span></span>
{:else if href}
  <a class="env-mark" {href} data-testid="env-state-mark" data-state={state} aria-label={word ? undefined : label} title={label}><span class="ev-dot s-{state}" aria-hidden="true"></span>{#if word}<span class="env-word">{label}</span>{/if}</a>
{:else if word}
  <span class="env-mark" data-testid="env-state-mark" data-state={state}><span class="ev-dot s-{state}" aria-hidden="true"></span><span class="env-word">{label}</span></span>
{:else}
  <span class="env-mark" role="img" data-testid="env-state-mark" data-state={state} aria-label={label} title={label}><span class="ev-dot s-{state}" aria-hidden="true"></span></span>
{/if}

<style>
  .env-mark { display: inline-flex; align-items: center; gap: var(--sp-1); min-inline-size: 0; color: var(--c-text-2); }
  .env-word { font-size: var(--text-xs); }
  .ev-dot { display: inline-block; flex: none; width: var(--ev-d, 12px); height: var(--ev-d, 12px);
    padding: 0; border: 2px solid transparent; border-radius: 50%; background: none; box-sizing: border-box; }
  .s-verified { background: var(--c-ok); }
  .s-waiting { background: var(--c-warn); }
  /* The board turns a square; a figure primitive rotates nothing (SPEC 3), so the diamond is clipped. */
  .s-failed { background: var(--c-err); border-radius: 0; clip-path: polygon(50% 0, 100% 50%, 50% 100%, 0 50%); }
  .s-frozen { background: var(--c-warn); border-radius: 2px; }
  .s-autohold { background: var(--c-info); border-radius: 2px; }
  .s-unverified { border-color: var(--c-text-3); }
  .s-empty { border: 2px dashed var(--c-text-3); }
  .s-unknown { border-color: var(--c-text-3); background: repeating-linear-gradient(45deg, var(--c-text-3) 0 2px, transparent 2px 4px); }
</style>
