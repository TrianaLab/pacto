<script>
  // A SHORT definition, on demand (requirement 14).
  //
  // The rule this component exists to enforce is the one in the requirement: hover is
  // supplementary, never the sole access path. So this is a real <button> -- reachable
  // by Tab, operable by Enter/Space, targetable by touch -- that hover merely
  // anticipates. Escape closes it, a click outside closes it, and it never traps focus.
  //
  // What belongs here: a definition of a term the page uses ("Revision match", "Blast
  // radius"). What does NOT: a warning, an uncertainty, or anything actionable. Those
  // are page content, and requirement 14 forbids putting them behind a hover.
  let { label = '', text = '' } = $props();

  let open = $state(false);
  let host = $state(null);

  // Hover ANTICIPATES the intent; it does not own it. Pointer-out closes only what
  // pointer-in opened, so a tip opened by keyboard or touch does not vanish when the
  // mouse happens to cross it.
  let byPointer = $state(false);

  function show(pointer) { open = true; byPointer = pointer; }
  function hide() { open = false; byPointer = false; }
  function toggle() { open ? hide() : show(false); }

  function onPointerLeave() { if (byPointer) hide(); }

  function onKeydown(e) {
    if (e.key === 'Escape' && open) { e.stopPropagation(); hide(); }
  }

  // Clicking anywhere else dismisses it. Without this a touch user who opened a tip has
  // no way to close it except finding the button again.
  $effect(() => {
    if (!open) return;
    const away = (e) => { if (host && !host.contains(e.target)) hide(); };
    document.addEventListener('pointerdown', away, true);
    return () => document.removeEventListener('pointerdown', away, true);
  });

  const id = $props.id();
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<span
  class="help"
  bind:this={host}
  onmouseenter={() => show(true)}
  onmouseleave={onPointerLeave}
  onkeydown={onKeydown}
>
  <button
    type="button"
    class="help-btn"
    aria-label={label ? `What is ${label}?` : 'What is this?'}
    aria-expanded={open}
    aria-describedby={open ? id : undefined}
    onclick={toggle}
    onfocus={() => show(false)}
    onblur={hide}
  >?</button>
  {#if open}
    <span class="help-body t-body-2" {id} role="note">{text}</span>
  {/if}
</span>

<style>
  .help { position: relative; display: inline-flex; align-items: center; }
  .help-btn {
    /* The touch target is the full 40px minimum; the visible dot stays small so a help
       affordance never competes with the title it sits beside. */
    width: var(--touch-min); height: var(--touch-min);
    display: inline-flex; align-items: center; justify-content: center;
    margin: calc((var(--touch-min) - 18px) / -2);
    background: none; border: 0; padding: 0; cursor: pointer; color: inherit;
  }
  .help-btn::before {
    content: '?';
    width: 16px; height: 16px; border-radius: var(--radius-pill);
    display: inline-flex; align-items: center; justify-content: center;
    border: 1px solid var(--c-border); background: var(--c-surface-inset);
    color: var(--c-text-3); font-size: var(--text-xs); line-height: 1;
  }
  /* The literal "?" in the button is replaced by the ::before glyph; keeping it in the
     DOM would read it out twice under a screen reader that ignores aria-label. */
  .help-btn { font-size: 0; }
  .help-btn:hover::before, .help-btn:focus-visible::before { border-color: var(--c-accent); color: var(--c-accent); }
  .help-body {
    position: absolute; top: calc(100% + 4px); left: 0; z-index: 30;
    width: max-content; max-width: min(38ch, 78vw);
    padding: var(--sp-2) var(--sp-3);
    background: var(--c-surface-raised); color: var(--c-text-2);
    border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    box-shadow: var(--shadow-md);
    text-transform: none; letter-spacing: normal;
  }
</style>
