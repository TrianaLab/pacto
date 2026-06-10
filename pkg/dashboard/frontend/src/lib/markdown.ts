/**
 * Render in-bundle Markdown to sanitized HTML for display in the dashboard.
 *
 * Bundle docs can originate from arbitrary OCI registries, so the output is run
 * through DOMPurify (no script execution, no event-handler attributes) before it
 * is injected with {@html}. Anchors are forced to open in a new tab with a safe
 * rel, since rendered content may link anywhere.
 */
import { marked } from 'marked';
import DOMPurify from 'dompurify';

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.nodeName === 'A') {
    node.setAttribute('target', '_blank');
    node.setAttribute('rel', 'noopener noreferrer');
  }
});

export function renderMarkdown(src: string): string {
  const raw = marked.parse(src ?? '', { async: false }) as string;
  return DOMPurify.sanitize(raw);
}
