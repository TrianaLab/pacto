// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { renderMarkdown } from './markdown.ts';

describe('renderMarkdown', () => {
  it('renders headings', () => {
    expect(renderMarkdown('# Title')).toContain('<h1>Title</h1>');
  });

  it('renders emphasis and lists', () => {
    const html = renderMarkdown('- **bold**\n- plain');
    expect(html).toContain('<li>');
    expect(html).toContain('<strong>bold</strong>');
  });

  it('strips <script> tags', () => {
    const html = renderMarkdown('hi <script>alert(1)</script>');
    expect(html).not.toContain('<script');
    expect(html).not.toContain('alert(1)');
  });

  it('strips event-handler attributes', () => {
    const html = renderMarkdown('<img src=x onerror="alert(1)">');
    expect(html).not.toContain('onerror');
  });

  it('forces safe link target/rel on anchors', () => {
    const html = renderMarkdown('[link](https://example.com)');
    expect(html).toContain('href="https://example.com"');
    expect(html).toContain('rel="noopener noreferrer"');
    expect(html).toContain('target="_blank"');
  });

  it('handles empty/undefined input', () => {
    expect(renderMarkdown('')).toBe('');
    // @ts-expect-error exercising the nullish guard
    expect(renderMarkdown(undefined)).toBe('');
  });
});
