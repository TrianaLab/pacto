/**
 * Component render tests for ServiceDetailView.svelte.
 * Verifies section collapse defaults and sticky TOC rail behavior.
 */
import { describe, it, expect } from 'vitest';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
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

/**
 * The contents rail lists DOMAIN_SECTIONS unconditionally, so every entry has to
 * be a heading that is actually on the page — otherwise the rail offers a jump
 * target that scrolls nowhere.
 *
 * That held only by accident until now: presence came from a per-section
 * `sectionMeta` map the backend stopped populating when the multi-source
 * resolver was retired, and this host (the `pacto doc` export) never had one at
 * all. Every section read back "present", so the placeholder branch never ran
 * and an empty section rendered nothing while the rail still advertised it.
 *
 * The fix moved the decision into each section, which owns the one predicate for
 * "has content" that also draws it. These two assertions are what keep it there:
 * the rail's ids must exist in the body, and the component behind each id must
 * have a placeholder to fall back on.
 */
describe('ServiceDetailView — every listed section is on the page', () => {
  const SRC = join(process.cwd(), 'src'); // vitest runs from the frontend package root
  const view = readFileSync(join(SRC, 'views/ServiceDetailView.svelte'), 'utf8');

  const block = view.match(/const DOMAIN_SECTIONS = \[([\s\S]*?)\];/)?.[1] ?? '';
  const ids = [...block.matchAll(/\{ id: '([^']+)'/g)].map((m) => m[1]);

  // Component tag rendering each id, e.g. `<SbomSection id="section-sbom" ...`.
  // The gap excludes `<` so the match cannot start at an earlier tag and reach
  // across it; DependenciesSection wraps its props, so it does span newlines.
  const tagFor = (id: string) =>
    view.match(new RegExp(`<([A-Z]\\w+)[^<]{0,400}?id="section-${id}"`))?.[1];

  it('finds the section list', () => {
    expect(ids).toHaveLength(11);
    expect(ids).toContain('sbom');
  });

  for (const id of ids) {
    it(`renders a component for "${id}" that falls back to a placeholder`, () => {
      const tag = tagFor(id);
      expect(tag, `DOMAIN_SECTIONS lists "${id}" but no component renders id="section-${id}"`).toBeDefined();

      const body = readFileSync(join(SRC, 'sections', `${tag}.svelte`), 'utf8');
      expect(
        body,
        `${tag} can render nothing, but the contents rail always offers "${id}"`,
      ).toContain('<SectionState');
    });
  }

  // Non-vacuous: the tag lookup finds real components, not undefined for all of
  // them, and it would notice an id the template never renders.
  it('is not vacuous', () => {
    expect(tagFor('sbom')).toBe('SbomSection');
    expect(tagFor('no-such-section')).toBeUndefined();
  });
});
