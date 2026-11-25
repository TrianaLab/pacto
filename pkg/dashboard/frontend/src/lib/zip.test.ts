import { describe, it, expect } from 'vitest';
import { crc32, zip } from './zip.ts';

const enc = new TextEncoder();
const dec = new TextDecoder();

// unzip reads a STORE archive back through its central directory, the way an unzip
// tool finds its entries, and checks every local header against it.
function unzip(b: Uint8Array): { path: string; text: string; crc: number }[] {
  const v = new DataView(b.buffer, b.byteOffset, b.byteLength);
  const end = b.length - 22;
  expect(v.getUint32(end, true)).toBe(0x06054b50);
  const n = v.getUint16(end + 10, true);
  let at = v.getUint32(end + 16, true);
  expect(at + v.getUint32(end + 12, true)).toBe(end);
  const out = [];
  for (let i = 0; i < n; i++) {
    expect(v.getUint32(at, true)).toBe(0x02014b50);
    expect(v.getUint16(at + 10, true)).toBe(0); // stored
    const crc = v.getUint32(at + 16, true);
    const size = v.getUint32(at + 24, true);
    const nameLen = v.getUint16(at + 28, true);
    const local = v.getUint32(at + 42, true);
    const path = dec.decode(b.subarray(at + 46, at + 46 + nameLen));
    expect(v.getUint32(local, true)).toBe(0x04034b50);
    expect(v.getUint32(local + 14, true)).toBe(crc);
    expect(v.getUint32(local + 18, true)).toBe(size);
    expect(v.getUint16(local + 26, true)).toBe(nameLen);
    const start = local + 30 + nameLen;
    const data = b.subarray(start, start + size);
    out.push({ path, text: dec.decode(data), crc });
    expect(crc32(data)).toBe(crc);
    at += 46 + nameLen;
  }
  return out;
}

describe('zip', () => {
  it('computes the IEEE CRC-32', () => {
    expect(crc32(enc.encode('hello'))).toBe(0x3610a686);
    expect(crc32(new Uint8Array())).toBe(0);
  });

  it('round-trips two files through the local headers and the central directory', () => {
    const files = [
      { path: 'pacto.yaml', text: 'pactoVersion: "2.5"\nservice:\n  name: invoices\n' },
      { path: 'docs/ünïcode.md', text: '# Invoices ✓\n' },
    ];
    const got = unzip(zip(files));
    expect(got.map(({ path, text }) => ({ path, text }))).toEqual(files);
    expect(got[0].crc).toBe(crc32(enc.encode(files[0].text)));
  });

  it('writes an empty archive as its end record alone', () => {
    expect(unzip(zip([]))).toEqual([]);
  });
});
