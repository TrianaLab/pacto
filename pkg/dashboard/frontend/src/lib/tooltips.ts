/**
 * Auto-aligns [data-tip] CSS tooltips so they never run off the left/right
 * viewport edge (the page is overflow-x:clip, so a spill would be silently cut).
 *
 * The tooltip renders centered under its host via CSS. When the host is near an
 * edge, that centered box would overflow — so on hover/focus we measure the host
 * and set data-tip-align="left|right" to anchor the tooltip to the edge instead.
 * Elements that hard-code data-tip-align keep their manual value untouched.
 */

// Half the tooltip's max-width (min(320px, 90vw)); the estimate only decides
// which elements are "near an edge", so being approximate is fine.
const HALF_WIDTH = 160;
const MARGIN = 8;

function place(el: Element): void {
  // Respect a manually-authored alignment; only manage ones we set ourselves.
  if (el.hasAttribute('data-tip-align') && !(el as HTMLElement).dataset.tipAuto) return;

  const r = el.getBoundingClientRect();
  const center = r.left + r.width / 2;
  let align = '';
  if (center + HALF_WIDTH > window.innerWidth - MARGIN) align = 'right';
  else if (center - HALF_WIDTH < MARGIN) align = 'left';

  if (align) {
    el.setAttribute('data-tip-align', align);
    (el as HTMLElement).dataset.tipAuto = '1';
  } else if ((el as HTMLElement).dataset.tipAuto) {
    el.removeAttribute('data-tip-align');
    delete (el as HTMLElement).dataset.tipAuto;
  }
}

function onEnter(e: Event): void {
  const el = (e.target as Element | null)?.closest?.('[data-tip]');
  if (el) place(el);
}

/** Wire up delegated listeners once; returns a cleanup function. */
export function initTooltipPlacement(): () => void {
  document.addEventListener('mouseover', onEnter, true);
  document.addEventListener('focusin', onEnter, true);
  return () => {
    document.removeEventListener('mouseover', onEnter, true);
    document.removeEventListener('focusin', onEnter, true);
  };
}
