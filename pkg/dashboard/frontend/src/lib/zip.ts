/**
 * zip writes a STORE-only zip archive of text files in the browser, so the Create
 * service handover downloads the files the answer already carries: no route reads a
 * bundle for it.
 */

// ponytail: STORE only (no compression) and no zip64; the answer carries at most 64
// files of 64 KiB, far under both ceilings. A zip library if the handover ever grows.

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

/** crc32 is the zip (IEEE) CRC-32 of b. */
export function crc32(b: Uint8Array): number {
  let c = 0xffffffff;
  for (const x of b) c = CRC_TABLE[(c ^ x) & 0xff] ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

// 1980-01-01, the earliest DOS date: the archive carries no real time.
const DOS_DATE = (1 << 5) | 1;
const UTF8_NAMES = 0x0800;

/** zip is the archive of files, each stored under its path, names in UTF-8. */
export function zip(files: readonly { path: string; text: string }[]): Uint8Array {
  const enc = new TextEncoder();
  const locals: Uint8Array[] = [];
  const centrals: Uint8Array[] = [];
  let offset = 0;
  for (const f of files) {
    const name = enc.encode(f.path);
    const data = enc.encode(f.text);
    const crc = crc32(data);
    const local = new Uint8Array(30 + name.length + data.length);
    const l = new DataView(local.buffer);
    l.setUint32(0, 0x04034b50, true);
    l.setUint16(4, 20, true);
    l.setUint16(6, UTF8_NAMES, true);
    l.setUint16(12, DOS_DATE, true);
    l.setUint32(14, crc, true);
    l.setUint32(18, data.length, true);
    l.setUint32(22, data.length, true);
    l.setUint16(26, name.length, true);
    local.set(name, 30);
    local.set(data, 30 + name.length);
    const central = new Uint8Array(46 + name.length);
    const c = new DataView(central.buffer);
    c.setUint32(0, 0x02014b50, true);
    c.setUint16(4, 20, true);
    c.setUint16(6, 20, true);
    c.setUint16(8, UTF8_NAMES, true);
    c.setUint16(14, DOS_DATE, true);
    c.setUint32(16, crc, true);
    c.setUint32(20, data.length, true);
    c.setUint32(24, data.length, true);
    c.setUint16(28, name.length, true);
    c.setUint32(42, offset, true);
    central.set(name, 46);
    locals.push(local);
    centrals.push(central);
    offset += local.length;
  }
  const size = centrals.reduce((a, b) => a + b.length, 0);
  const end = new Uint8Array(22);
  const e = new DataView(end.buffer);
  e.setUint32(0, 0x06054b50, true);
  e.setUint16(8, files.length, true);
  e.setUint16(10, files.length, true);
  e.setUint32(12, size, true);
  e.setUint32(16, offset, true);
  const out = new Uint8Array(offset + size + end.length);
  let at = 0;
  for (const part of [...locals, ...centrals, end]) {
    out.set(part, at);
    at += part.length;
  }
  return out;
}
