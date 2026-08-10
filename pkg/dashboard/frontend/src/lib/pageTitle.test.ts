import { describe, it, expect, afterEach } from 'vitest';
import { titleFor, syncPageTitle } from './pageTitle.ts';

describe('titleFor', () => {
  it('names the page and keeps the product recognizable', () => {
    expect(titleFor('Needs attention')).toBe('Needs attention - Pacto');
  });

  it('collapses the whitespace a multi-line heading carries', () => {
    // An h1 built from several elements arrives as "Revision:\n  payments 2.1.0".
    expect(titleFor('Revision:\n  payments-service 2.1.0 ')).toBe('Revision: payments-service 2.1.0 - Pacto');
  });

  it('falls back rather than emitting an empty or duplicated title', () => {
    expect(titleFor('')).toBe('Pacto Dashboard');
    expect(titleFor(null)).toBe('Pacto Dashboard');
    expect(titleFor(undefined)).toBe('Pacto Dashboard');
    expect(titleFor('   ')).toBe('Pacto Dashboard');
    expect(titleFor('Pacto')).toBe('Pacto Dashboard');
    expect(titleFor('Pacto Dashboard')).toBe('Pacto Dashboard');
  });
});

describe('syncPageTitle', () => {
  const roots: Element[] = [];
  const mount = (html: string) => {
    const el = document.createElement('div');
    el.innerHTML = html;
    document.body.appendChild(el);
    roots.push(el);
    return el;
  };

  afterEach(() => {
    roots.splice(0).forEach((r) => r.remove());
    document.title = '';
  });

  it('mirrors the heading that is already rendered', () => {
    syncPageTitle(mount('<h1>Operational graph</h1>'));
    expect(document.title).toBe('Operational graph - Pacto');
  });

  it('picks up a heading that only arrives once the data lands', async () => {
    // This is the whole reason for the observer: a detail route renders its shell
    // first and its real h1 only after the entity request resolves.
    const root = mount('<p>Loading</p>');
    syncPageTitle(root);
    expect(document.title).toBe('Pacto Dashboard');

    root.innerHTML = '<h1>Service: payments-service</h1>';
    await new Promise((r) => setTimeout(r, 0));
    expect(document.title).toBe('Service: payments-service - Pacto');
  });

  it('follows a heading that is edited in place', async () => {
    const root = mount('<h1>Overview</h1>');
    syncPageTitle(root);

    root.querySelector('h1')!.textContent = 'Change analysis';
    await new Promise((r) => setTimeout(r, 0));
    expect(document.title).toBe('Change analysis - Pacto');
  });

  it('stops writing once torn down', async () => {
    const root = mount('<h1>Overview</h1>');
    syncPageTitle(root)();

    root.querySelector('h1')!.textContent = 'Services';
    await new Promise((r) => setTimeout(r, 0));
    expect(document.title).toBe('Overview - Pacto');
  });

  it('is a no-op without a root, so an unmounted caller cannot crash', () => {
    expect(() => syncPageTitle(null)()).not.toThrow();
  });
});
