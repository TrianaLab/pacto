/**
 * SVG icon markup (inner elements only) per readiness category, keyed by the
 * canonical category slugs from pkg/contract. Stroke-style on a 24×24 viewBox —
 * set `fill="none"` + `stroke`/`stroke-width` on the PARENT <svg> (the children
 * inherit). Unknown / custom categories fall back to the 'other' tag icon.
 *
 * Shared by the D3 charts (heatmap header) and Svelte components (CategoryIcon)
 * so the category iconography is defined once. The icon set was reviewed visually.
 */

const ICONS: Record<string, string> = {
  architecture: '<rect x="3" y="3" width="7" height="9" rx="1"/><rect x="14" y="3" width="7" height="5" rx="1"/><rect x="14" y="12" width="7" height="9" rx="1"/><rect x="3" y="16" width="7" height="5" rx="1"/>',
  testing: '<path d="M9 3h6"/><path d="M10 3v6.5L4.6 18.4A2 2 0 0 0 6.3 21h11.4a2 2 0 0 0 1.7-2.6L14 9.5V3"/><path d="M7.5 15h9"/>',
  'code-quality': '<path d="m16 18 6-6-6-6"/><path d="m8 6-6 6 6 6"/>',
  observability: '<path d="M22 12h-4l-3 9L9 3l-3 9H2"/>',
  security: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/><path d="m9 12 2 2 4-4"/>',
  documentation: '<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><path d="M14 2v6h6"/><path d="M9 13h6"/><path d="M9 17h6"/>',
  infrastructure: '<rect x="2" y="3" width="20" height="8" rx="2"/><rect x="2" y="13" width="20" height="8" rx="2"/><path d="M6 7h.01"/><path d="M6 17h.01"/>',
  'ci-cd': '<path d="m17 2 4 4-4 4"/><path d="M3 11v-1a4 4 0 0 1 4-4h14"/><path d="m7 22-4-4 4-4"/><path d="M21 13v1a4 4 0 0 1-4 4H3"/>',
  deployment: '<path d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09z"/><path d="M12 15l-3-3a22 22 0 0 1 2-3.95A12.88 12.88 0 0 1 22 2c0 2.72-.78 7.5-6 11a22.35 22.35 0 0 1-4 2z"/><path d="M9 12H4s.55-3.03 2-4c1.62-1.08 5 0 5 0"/><path d="M12 15v5s3.03-.55 4-2c1.08-1.62 0-5 0-5"/>',
  resilience: '<circle cx="12" cy="12" r="10"/><circle cx="12" cy="12" r="4"/><path d="m4.9 4.9 4.2 4.2"/><path d="m14.9 14.9 4.2 4.2"/><path d="m14.9 9.1 4.2-4.2"/><path d="m4.9 19.1 4.2-4.2"/>',
  'backup-recovery': '<ellipse cx="12" cy="5" rx="9" ry="3"/><path d="M3 5v14a9 3 0 0 0 18 0V5"/><path d="M3 12a9 3 0 0 0 18 0"/>',
  'incident-response': '<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3z"/><path d="M12 9v4"/><path d="M12 17h.01"/>',
  compliance: '<rect x="8" y="2" width="8" height="4" rx="1"/><path d="M16 4h2a2 2 0 0 1 2 2v14a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2h2"/><path d="m9 14 2 2 4-4"/>',
  other: '<path d="M12.59 13.41 20 6l-8.59-.01a2 2 0 0 0-1.41.58l-6 6a2 2 0 0 0 0 2.83l4.59 4.59a2 2 0 0 0 2.83 0l6-6"/><path d="M7 7h.01"/>',
};

/** Inner SVG markup for a category's icon; unknown categories fall back to 'other'. */
export function categoryIconInner(category: string): string {
  return ICONS[category] || ICONS.other;
}

/** True if the category has a dedicated (non-fallback) icon. */
export function isKnownCategory(category: string): boolean {
  return Object.prototype.hasOwnProperty.call(ICONS, category);
}
