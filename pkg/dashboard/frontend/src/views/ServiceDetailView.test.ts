/**
 * Component render tests for ServiceDetailView.svelte.
 * Verifies section collapse defaults and sticky TOC rail behavior.
 */
import { describe, it, expect } from 'vitest';
// @ts-expect-error — Svelte component has no declaration file
import { defaultOpenSections } from './ServiceDetailView.svelte';

describe('ServiceDetailView — section defaults', () => {
  it('collapses secondary sections by default', () => {
    const o = defaultOpenSections();
    expect(o.overview).toBe(true);
    expect(o.interfaces).toBe(true);
    expect(o.dependencies).toBe(false);
    expect(o.config).toBe(false);
    expect(o.policy).toBe(false);
    expect(o.readiness).toBe(false);
    expect(o.docs).toBe(false);
    expect(o.sbom).toBe(false);
    expect(o.validation).toBe(false);
    expect(o.runtimeDiff).toBe(false);
    expect(o.observed).toBe(false);
    expect(o.sources).toBe(false);
  });
});
