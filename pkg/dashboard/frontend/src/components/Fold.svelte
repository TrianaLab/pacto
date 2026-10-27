<script>
  // A section folded behind its title and a short note of what is inside: the
  // why, the proof, the details and the timeline of one change sit here, so
  // the page leads with its one question. A real <details>, so the keyboard
  // and a screen reader open it like any disclosure. `open` is only where it
  // starts: a fold holding a problem starts open, because a problem never
  // hides behind a click.
  let { title = '', meta = '', id = '', open = false, testid = '', level = 2, labelledby = '', children } = $props();
</script>

<details class="disclosure fold" {open} data-testid={testid || undefined} aria-labelledby={labelledby || undefined}>
  <summary><span class="disclosure-caret" data-motion aria-hidden="true">&#9656;</span><svelte:element this={`h${level}`} class="fold-title" id={id || undefined}>{title}</svelte:element>{#if meta}<span class="t-meta fold-meta">{meta}</span>{/if}</summary>
  <div class="fold-body">{@render children?.()}</div>
</details>

<style>
  .fold { border-top: 1px solid var(--c-border); }
  .fold:last-of-type { border-bottom: 1px solid var(--c-border); }
  .fold > summary { gap: var(--sp-2); color: var(--c-text); font-size: var(--text-base); }
  .fold-title { margin: 0; font: inherit; font-weight: 500; letter-spacing: normal; }
  .fold-meta { margin-left: auto; color: var(--c-text-3); }
  .fold-body { display: flex; flex-direction: column; gap: var(--sp-2); padding: 0 0 var(--sp-3) var(--sp-4); }
</style>
