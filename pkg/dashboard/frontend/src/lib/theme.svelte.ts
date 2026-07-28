/**
 * Reactive theme store using Svelte 5 runes.
 *
 * The theme lives as a `data-theme` attribute on <html> (set early in index.html to
 * avoid a flash of the wrong theme). D3 charts read their colors from CSS custom
 * properties that switch on `[data-theme]`, so they must re-render when the theme
 * changes. Reading `currentTheme()` inside a chart's `$effect` makes that effect
 * reactively depend on the theme, so a toggle re-runs it — a raw
 * `document.documentElement.getAttribute('data-theme')` read does not.
 */

let theme = $state<string>(readInitialTheme());

function readInitialTheme(): string {
  if (typeof document === 'undefined') return 'dark';
  const attr = document.documentElement.getAttribute('data-theme');
  if (attr) return attr;
  if (typeof matchMedia === 'function' && matchMedia('(prefers-color-scheme: dark)').matches) {
    return 'dark';
  }
  return 'light';
}

/** Get the current theme name (reactive). */
export function currentTheme(): string {
  return theme;
}

/** Toggle between dark and light, updating the DOM attribute and localStorage. */
export function toggleTheme(): void {
  const next = theme === 'dark' ? 'light' : 'dark';
  theme = next;
  if (typeof document !== 'undefined') {
    document.documentElement.setAttribute('data-theme', next);
  }
  try {
    localStorage.setItem('pacto-theme', next);
  } catch {
    // localStorage may be unavailable (private mode); the DOM attribute is enough.
  }
}
