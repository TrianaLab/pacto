<script>
  // The B2 sidebar (SPEC 20): grouped, collapsible to a 60px icon rail.
  //   >= 1200px  it pushes the content; the reader's choice is stored per browser, and
  //              with none it starts expanded from 1440px and collapsed below.
  //   768-1199   it always starts collapsed; expanding opens a sheet over the content
  //              that is never stored, and Escape, a click outside, a link, leaving it
  //              with Tab or a route change closes it (Escape returns focus).
  //   < 768      it is gone; "[" opens the top bar's Menu drawer instead.
  import NavList from './NavList.svelte';

  let { groups = [], view = 'list', entityKind = '', attentionTotal = null, sourceCounts = null, blocked = false } = $props();

  const KEY = 'pacto-nav';
  function readStored() {
    try { return localStorage.getItem(KEY); } catch { return null; }
  }

  let vw = $state(typeof window === 'undefined' ? 1440 : window.innerWidth);
  let stored = $state(readStored());
  let over = $state(false);
  let anim = $state(false);
  let sideEl = $state(null);
  let toggleEl = $state(null);

  const mode = $derived(vw < 768 ? 'none'
    : vw < 1200 ? (over ? 'over' : 'rail')
    : (stored ? stored === 'expanded' : vw >= 1440) ? 'open' : 'rail');
  const expanded = $derived(mode === 'open' || mode === 'over');

  // The sheet belongs to the 768-1199 band only.
  $effect(() => { if (vw >= 1200 || vw < 768) over = false; });

  let animTimer = 0;
  function toggle() {
    if (mode === 'none') { document.getElementById('nav-menu')?.click(); return; }
    const want = !expanded;
    // The width animates only on a toggle, never on a window resize.
    anim = true;
    clearTimeout(animTimer);
    animTimer = setTimeout(() => { anim = false; }, 300);
    if (vw < 1200) { over = want; return; }
    stored = want ? 'expanded' : 'collapsed';
    try { localStorage.setItem(KEY, stored); } catch { /* private mode: the choice lasts this page */ }
  }

  function closeOver(focus) {
    if (mode !== 'over') return;
    over = false;
    if (focus) toggleEl?.focus();
  }

  function isTyping(t) {
    return t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.tagName === 'SELECT' || t.isContentEditable);
  }

  function onKeydown(e) {
    if (e.key === 'Escape' && mode === 'over') { closeOver(true); return; }
    if (e.key !== '[' || e.ctrlKey || e.metaKey || e.altKey || blocked || isTyping(e.target)) return;
    e.preventDefault();
    toggle();
  }

  function onPointerdown(e) {
    if (mode === 'over' && sideEl && !sideEl.contains(e.target)) closeOver(false);
  }

  function onFocusout(e) {
    if (!sideEl?.contains(e.relatedTarget)) closeOver(false);
  }

  // The rail scrolls inside itself, so its tooltips are fixed: an item hands its
  // position to its tooltip on hover and on focus.
  function placeTip(e) {
    const it = e.target.closest?.('.nav-item, .side-toggle');
    if (!it) return;
    const r = it.getBoundingClientRect();
    it.style.setProperty('--tip-x', `${r.right + 10}px`);
    it.style.setProperty('--tip-y', `${r.top + r.height / 2}px`);
  }
</script>

<svelte:window bind:innerWidth={vw} onkeydown={onKeydown} onhashchange={() => closeOver(false)} />
<svelte:document onpointerdown={onPointerdown} />

{#if mode !== 'none'}
  <!-- svelte-ignore a11y_no_static_element_interactions (the region only positions tooltips and closes the sheet; its links and button are the widgets) -->
  <div class="side" id="primary-sidebar" data-rail={mode} class:anim bind:this={sideEl} data-testid="sidebar"
    onfocusout={onFocusout} onpointerover={placeTip} onfocusin={placeTip}>
    <div class="side-in">
      <button type="button" class="side-toggle" bind:this={toggleEl} onclick={toggle}
        aria-controls="primary-sidebar" aria-expanded={expanded} aria-keyshortcuts="[">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false"><rect x="3.5" y="4.5" width="17" height="15" rx="2" /><path d="M9.5 4.5 V19.5 M15.5 9.5 L13 12 L15.5 14.5" /></svg>
        <span class="side-tip">Sidebar<kbd aria-hidden="true">[</kbd></span>
      </button>
      <NavList {groups} {view} {entityKind} {attentionTotal} {sourceCounts} prefix="s" onNavigate={() => closeOver(false)} />
    </div>
  </div>
{/if}

<style>
  /* The rail and its rule run the full dynamic viewport height under the top bar, on
     pages shorter than the window too (SPEC 22.4, 24.4). --shell-top-inset is the height
     of a host band above the top bar while it is on screen (the WASM demo's notice). */
  .side {
    position: sticky; top: var(--navbar-h); align-self: start; z-index: 20;
    block-size: calc(100vh - var(--navbar-h) - var(--shell-top-inset, 0px)); block-size: calc(100dvh - var(--navbar-h) - var(--shell-top-inset, 0px));
    inline-size: 232px;
  }
  .side.anim, .side.anim .side-in { transition: inline-size var(--dur-3) var(--ease-in-place); }
  .side-in {
    block-size: 100%; inline-size: 100%;
    overflow: hidden auto; overscroll-behavior: contain; scrollbar-width: thin;
    background: var(--c-surface); border-right: 1px solid var(--c-border);
    padding: var(--sp-2) var(--sp-2) max(var(--sp-6), env(safe-area-inset-bottom)) max(var(--sp-2), env(safe-area-inset-left));
  }
  .side[data-rail='rail'], .side[data-rail='over'] { inline-size: 60px; }
  /* The sheet: the column stays 60px and the panel lies over the content. */
  .side[data-rail='over'] .side-in { inline-size: 232px; box-shadow: var(--shadow-md); }

  .side-toggle {
    position: relative; display: inline-flex; align-items: center; justify-content: center;
    width: 40px; height: 36px; margin: 0 0 var(--sp-1); padding: 0;
    background: none; border: 0; border-radius: var(--radius-sm); color: var(--c-text-2); cursor: pointer;
  }
  .side-toggle:hover { background: var(--c-surface-hover); color: var(--c-text); }
  .side-toggle svg { transition: transform var(--dur-3) var(--ease-in-place); }
  .side[data-rail='rail'] .side-toggle svg { transform: scaleX(-1); }
  .side-tip kbd { font: var(--text-xs) var(--font-mono); border: 1px solid var(--c-border); border-radius: 4px; padding: 0 5px; margin-left: var(--sp-2); }

  /* Visually hidden, still named: the toggle's tip always, and in the rail every label
     and group heading. */
  .side-tip,
  .side[data-rail='rail'] :global(.nav-label),
  .side[data-rail='rail'] :global(.nav-group-h) {
    position: absolute; left: 0; top: 0; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); white-space: nowrap;
  }
  .side[data-rail='rail'] :global(.nav-group + .nav-group) { margin-top: var(--sp-2); padding-top: var(--sp-2); border-top: 1px solid var(--c-border); }
  .side[data-rail='rail'] :global(.nav-item) { justify-content: center; padding: var(--sp-2) 0; }
  .side[data-rail='rail'] :global(.nav-cnt), .side[data-rail='rail'] :global(.nav-meta) { display: none; }
  .side[data-rail='rail'] :global(.nav-bdg) {
    display: block; position: absolute; top: 1px; right: 0;
    font: 700 var(--text-xs)/1.2 var(--font-sans); color: var(--c-err);
    background: var(--c-err-bg); border: 1px solid var(--c-err-border); border-radius: 999px; padding: 0 3px;
  }
  .side[data-rail='rail'] :global(.nav-tipx) { display: inline; font-weight: 400; color: var(--c-text-2); }
  /* The tooltip is the label itself, so the accessible name and the tip are the same words. */
  .side[data-rail='rail'] :global(.nav-item:is(:hover, :focus-visible) .nav-label),
  .side-toggle:is(:hover, :focus-visible) .side-tip {
    position: fixed; left: var(--tip-x, 72px); top: var(--tip-y, 50%); transform: translateY(-50%);
    width: auto; height: auto; overflow: visible; clip: auto; z-index: 30; pointer-events: none;
    font-size: var(--text-sm); font-weight: 500; line-height: 1.4; color: var(--c-text);
    background: var(--c-surface-raised); border: 1px solid var(--c-border); border-radius: var(--radius-sm);
    padding: var(--sp-1) var(--sp-2); box-shadow: var(--shadow-md);
  }
  @media (pointer: coarse) { .side-toggle { width: 44px; height: 44px; } }
</style>
