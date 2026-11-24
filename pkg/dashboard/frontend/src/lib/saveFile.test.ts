import { describe, it, expect, vi, afterEach } from 'vitest';
import { saveFile } from './saveFile.ts';

describe('saveFile', () => {
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

  it('clicks a temporary download link over a blob URL, then revokes it', () => {
    vi.useFakeTimers();
    const create = vi.fn(() => 'blob:x');
    const revoke = vi.fn();
    Object.assign(URL, { createObjectURL: create, revokeObjectURL: revoke });
    const clicked: { href: string; download: string }[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) { clicked.push({ href: this.href, download: this.download }); });
    saveFile('pacto-members-argocd.yaml', 'registry: r\n');
    expect(clicked).toEqual([{ href: 'blob:x', download: 'pacto-members-argocd.yaml' }]);
    expect((create.mock.calls[0] as unknown as [Blob])[0].type).toBe('application/yaml');
    expect(document.querySelector('a[download]')).toBeNull();
    expect(revoke).not.toHaveBeenCalled();
    vi.advanceTimersByTime(1000);
    expect(revoke).toHaveBeenCalledWith('blob:x');
  });
});
