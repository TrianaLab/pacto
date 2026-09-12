#!/usr/bin/env node
// apply-release-plan.mjs — consume release/release-plan.json and MUTATE the working
// tree into release state, idempotently, then validate every mutated field.
//
// Every edit is keyed to a GENERIC pattern (a version regex, not the old value),
// so re-running produces a byte-identical tree: run twice -> the second `git diff`
// is empty. It also emits release/release-manifest.json (every release unit ->
// {version, coordinate, tag, artifactKind}).
import { readFileSync, writeFileSync, readdirSync, existsSync, mkdtempSync } from 'node:fs';
import { execFileSync } from 'node:child_process';
import { tmpdir } from 'node:os';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const R = (...p) => join(root, ...p);
const plan = JSON.parse(readFileSync(R('release', 'release-plan.json'), 'utf8'));

// ---- values from the plan (single source of truth) ----
const core = plan.groups.core;
const k8s = plan.groups.kubernetes;
const chart = k8s.artifacts.find((a) => a.kind === 'helm-chart');
const opImage = k8s.artifacts.find((a) => a.unit === 'operator-image');
const chartVersion = chart.chartVersion;      // e.g. 4.7.0
const appVersion = chart.appVersion;          // e.g. 4.7.0
const imageTag = chart.defaultImageTag;       // e.g. 4.7.0
const opImageCoord = opImage.coordinate;      // ghcr.io/.../pacto-controller
const compat = k8s.compatibility.pactoCore;   // e.g. >=2.0.0
const pin = k8s.goModPin;                      // { module, version:"v2.7.0" }
const assertNoReplace = k8s.assertNoReplace;   // release state must carry no replace

// Matches a semver core, optionally with a prerelease suffix.
const SEMVER = String.raw`\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?`;
const esc = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

const changed = [];
function edit(relPath, fn) {
  const p = R(relPath);
  const before = readFileSync(p, 'utf8');
  const after = fn(before);
  if (after !== before) { writeFileSync(p, after); changed.push(relPath); }
  return after;
}
function assert(cond, msg) {
  if (!cond) { console.error(`apply-release-plan: FAIL — ${msg}`); process.exit(1); }
}

// ---- 1. integration go.mod: pin published core; fail closed on any replace ----
// assertNoReplace declares the release-state invariant: a replace directive must
// NOT survive into a published go.mod. This is enforcement, not removal — a stray
// replace is a mistake to surface loudly, never something to silently rewrite.
//
// The pin has to name a version that CAN exist. Go's import-compat rule ties the
// halves together — a `/vN` path carries only vN.y.z, an unsuffixed path only
// v0/v1 — so `.../v3` at `v4.0.0` is not a require whose module is missing, it is
// one go refuses to parse. A major core bump produced exactly that, because the
// version advances and the module path does not; renaming the path is a separate,
// deliberate step. build-release-plan.mjs now refuses to emit such a plan, so this
// is unreachable through the release path and guards the hand-edited one.
const majorOf = (v) => Number(/^v(\d+)\./.exec(v)?.[1] ?? NaN);
const pathMajor = Number(/\/v(\d+)$/.exec(pin.module)?.[1] ?? 1);
assert(pathMajor >= 2 ? majorOf(pin.version) === pathMajor : majorOf(pin.version) <= 1,
  `release plan pins ${pin.module} at ${pin.version}, which that module path cannot carry ` +
  `(a /v${pathMajor} path requires v${pathMajor}.y.z). Rename the module path to ` +
  `/v${majorOf(pin.version)} before pinning this version — writing the require anyway ` +
  'produces a go.mod go cannot parse.');
{
  const rel = 'integrations/kubernetes/go.mod';
  const after = edit(rel, (s) =>
    s.replace(new RegExp(`${esc(pin.module)} v${SEMVER}`), `${pin.module} ${pin.version}`));
  assert(after.includes(`${pin.module} ${pin.version}`),
    `${rel}: core require not pinned to ${pin.version}`);
  if (assertNoReplace) {
    assert(!/^\s*replace(\s|\()/m.test(after),
      `${rel}: a replace directive is present — release state must have none`);
  }
}

// ---- 1a. integration go.sum: carry checksums for the core version we just pinned ----
// The operator is consumed STANDALONE — go.work's replace does not travel with it — so
// its committed go.sum must hold the checksums for the exact core version the require
// above names. Bumping the require without the sums leaves a module that cannot be built
// with the default `-mod=readonly`: `go build ./...` fails with "missing go.sum entry" for
// every pacto/v3 package. That is not hypothetical. Between 2026-09-07 and 2026-09-11 the
// require said v3.3.0 then v3.3.1 while the sums still said v3.2.7, and it shipped that way
// in the published integrations/kubernetes/v5.4.0 tag, because this script moved the pin
// and nothing moved the sums.
//
// Nothing downstream noticed: the operator Dockerfile, verify-standalone.sh and
// verify-k8s-standalone.sh all run with GOFLAGS=-mod=mod and GONOSUMDB for
// github.com/trianalab/*, which tells go to WRITE whatever entries are missing and skip the
// checksum database. Held by TestOperatorGoSumCoversThePinnedCoreVersion, which reads the
// two committed files and compares them with no flags in the way.
//
// The version being pinned is normally NOT published yet — this runs while generating the
// Version PR, before the tag exists — so the checksums cannot be fetched. They do not need
// to be: a go.sum hash is derived from module CONTENT, not from the commit or the tag, and
// the tree here is byte-identical to the tree the release will tag (squash-merge preserves
// it). So we tag the current HEAD locally, point git at this repository through a
// PROCESS-SCOPED config (never the user's global), and let `go mod download` compute the
// same hashes the proxy will later serve. Same technique as verify-standalone.sh, verified
// against the published v3.3.1: the local staging tag reproduced the proxy's h1 exactly.
//
// That equivalence holds only while HEAD is the tree the version will be tagged from, which
// is the release case and not the backfill case: pinning a version that is ALREADY published
// from an unrelated HEAD would mint a hash the proxy disagrees with, turning "missing go.sum
// entry" into a checksum MISMATCH — a security error, and a worse failure than the one this
// section exists to prevent. So the published module wins when it exists. The staging tag is
// the fallback, taken only when the proxy genuinely does not have this version.
//
// Skipped entirely when the entry is already present, which is every run between releases —
// including ci.mk's artifact-drift idempotency check, which re-runs this script and demands
// a byte-identical tree. Deterministic input, deterministic hash, so the re-run is a no-op.
// Section 1 has already proved the pin names a version this module path can carry, so the
// coordinate resolved below is one the proxy can actually be asked about.
{
  const rel = 'integrations/kubernetes/go.sum';
  const before = readFileSync(R(rel), 'utf8');
  if (!before.includes(`${pin.module} ${pin.version} h1:`)) {
    const opDir = R('integrations', 'kubernetes');
    // Section 1 already pinned the require, so go.mod is at its final state here.
    // `-mod=mod` below licenses go to rewrite it; nothing about resolving one
    // already-required module should, so treat any rewrite as a bug rather than
    // letting go quietly restate the operator's requirements mid-release.
    const goModBefore = readFileSync(R('integrations', 'kubernetes', 'go.mod'), 'utf8');
    const run = (cmd, args, opts = {}) =>
      execFileSync(cmd, args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'], ...opts });
    // A throwaway module cache per attempt: a hash must come from the source this run
    // resolved, never from whatever a previous run happened to leave behind.
    const download = (extra) => run('go', ['mod', 'download', pin.module], {
      cwd: opDir,
      env: {
        ...process.env,
        GOWORK: 'off', GOFLAGS: '-mod=mod',
        GOMODCACHE: mkdtempSync(join(tmpdir(), 'aprp-mod-')),
        ...extra,
      },
    });

    let published = true;
    try {
      // Authoritative path: the real proxy with the checksum database ON. Anything
      // inherited that would bypass either is cleared, so a published version can only
      // produce the hash the rest of the world will verify against.
      download({ GOPRIVATE: '', GONOPROXY: '', GONOSUMDB: '', GONOSUMCHECK: '' });
    } catch {
      published = false;
    }
    if (!published) {
      const gitConfig = join(mkdtempSync(join(tmpdir(), 'aprp-git-')), 'config');
      writeFileSync(gitConfig,
        `[url "file://${root}"]\n\tinsteadOf = https://github.com/trianalab/pacto\n`);
      // Unpublished, so the tag should not exist. If one does, it is the source of truth
      // for its own version — resolve through it rather than clobbering it.
      const staged = run('git', ['-C', root, 'tag', '-l', pin.version]).trim() !== '';
      try {
        if (!staged) run('git', ['-C', root, 'tag', pin.version, 'HEAD']);
        download({
          GIT_CONFIG_GLOBAL: gitConfig, GIT_CONFIG_SYSTEM: '/dev/null',
          GOPRIVATE: 'github.com/trianalab/*', GONOSUMDB: 'github.com/trianalab/*',
        });
      } finally {
        if (!staged) { try { run('git', ['-C', root, 'tag', '-d', pin.version]); } catch { /* best effort */ } }
      }
    }
    assert(readFileSync(R('integrations', 'kubernetes', 'go.mod'), 'utf8') === goModBefore,
      'integrations/kubernetes/go.mod: rewritten by `go mod download`; only its go.sum may move here');
    const after = readFileSync(R(rel), 'utf8');
    assert(after.includes(`${pin.module} ${pin.version} h1:`),
      `${rel}: no checksum for ${pin.module} ${pin.version} after go mod download`);
    assert(after.includes(`${pin.module} ${pin.version}/go.mod h1:`),
      `${rel}: no go.mod checksum for ${pin.module} ${pin.version} after go mod download`);
    // go rewrote the file on disk; record it the same way edit() would.
    if (after !== before) changed.push(rel);
  }
}

// ---- 1b. go.work: keep the versioned replace in lockstep with the pinned core ----
// The workspace resolves the not-yet-published core via a version-pinned replace
// (`<module> vX.Y.Z => .`). When the core version bumps, this pin MUST move with the
// go.mod require, or the workspace tries to fetch the unpublished new version and
// `go mod download` fails ("unknown revision"). Unlike the go.mod, a go.work replace
// is legitimate (it is the dev/CI workspace, never a published module).
{
  const rel = 'go.work';
  const after = edit(rel, (s) =>
    s.replace(new RegExp(`(replace ${esc(pin.module)} )v${SEMVER}( => \\.)`),
      `$1${pin.version}$2`));
  assert(after.includes(`replace ${pin.module} ${pin.version} => .`),
    `${rel}: versioned core replace not pinned to ${pin.version}`);
}

// ---- 2. operator chart Chart.yaml: version, appVersion, artifacthub image ----
{
  const rel = 'integrations/kubernetes/charts/pacto-operator/Chart.yaml';
  const after = edit(rel, (s) => {
    s = s.replace(/^version:.*$/m, `version: ${chartVersion}`);
    s = s.replace(/^appVersion:.*$/m, `appVersion: "${appVersion}"`);
    // Artifact Hub image annotation: add once, then keep its tag in sync.
    if (!s.includes('artifacthub.io/images')) {
      if (!s.endsWith('\n')) s += '\n';
      s += `annotations:\n  artifacthub.io/images: |\n    - name: pacto-controller\n` +
           `      image: ${opImageCoord}:${appVersion}\n`;
    }
    s = s.replace(new RegExp(`(${esc(opImageCoord)}:)v?${SEMVER}`), `$1${appVersion}`);
    return s;
  });
  assert(new RegExp(`^version: ${esc(chartVersion)}$`, 'm').test(after), `${rel}: version != ${chartVersion}`);
  assert(new RegExp(`^appVersion: "${esc(appVersion)}"$`, 'm').test(after), `${rel}: appVersion != ${appVersion}`);
  assert(after.includes(`${opImageCoord}:${appVersion}`), `${rel}: artifacthub image tag != ${appVersion}`);
}
  // image.tag intentionally NOT pinned in values.yaml: the chart deployment defaults
  // the tag to .Chart.AppVersion, so pinning it here would only create helm-docs drift.

// ---- 4. artifacthub-repo.yml consistency (repo metadata carries no version) ----
{
  const rel = 'integrations/kubernetes/artifacthub-repo.yml';
  assert(existsSync(R(rel)) && /repositoryID:/.test(readFileSync(R(rel), 'utf8')),
    `${rel}: missing or lacks repositoryID`);
}

// ---- 5. integration.yaml compatibility (pactoCore) ----
{
  const rel = 'integrations/kubernetes/integration.yaml';
  const after = edit(rel, (s) => s.replace(/(pactoCore:\s*)"[^"]*"/, `$1"${compat}"`));
  assert(after.includes(`pactoCore: "${compat}"`), `${rel}: pactoCore != ${compat}`);
}

// ---- 6. generated install examples: chart README badges + --version pin ----
{
  const rel = 'integrations/kubernetes/charts/pacto-operator/README.md';
  const after = edit(rel, (s) => {
    s = s.replace(new RegExp(`(--version )v?${SEMVER}`, 'g'), `$1${chartVersion}`);
    // helm-docs badges: alt text + shields.io URL, chart version and appVersion.
    s = s.replace(new RegExp(`(?<!App)Version: ${SEMVER}`, 'g'), `Version: ${chartVersion}`);
    s = s.replace(new RegExp(`(?<!App)Version-${SEMVER}-informational`, 'g'), `Version-${chartVersion}-informational`);
    s = s.replace(new RegExp(`AppVersion: ${SEMVER}`, 'g'), `AppVersion: ${appVersion}`);
    s = s.replace(new RegExp(`AppVersion-${SEMVER}-informational`, 'g'), `AppVersion-${appVersion}-informational`);
    return s;
  });
  assert(after.includes(`--version ${chartVersion}`), `${rel}: install snippet not pinned to ${chartVersion}`);
}

// ---- 6b. core docs: the image coordinates a reader copy-pastes ----
// Same failure the chart README's `--version` pin already had, on the other side
// of the repo: a tag written by hand goes stale at the next release and ships a
// command that resolves to nothing. Keyed to the coordinate rather than the old
// tag, so re-running is a no-op. This list is not the guarantee — docs_check (h)
// holds EVERY page to the published tag, so a doc pinning a coordinate this list
// has never heard of fails the gate instead of rotting.
{
  const images = Object.values(plan.groups)
    .flatMap((g) => g.artifacts)
    .filter((a) => a.kind === 'oci-image' && a.coordinate);
  const pages = [
    'docs/examples/compose-demo.md',
    'docs/dashboard-docker.md',
    // A transcript of the operator's own startup log, which names the dashboard
    // image it deploys. Sample output ages exactly like a command does.
    'integrations/kubernetes/docs/installation.md',
  ];
  for (const rel of pages) {
    const after = edit(rel, (s) => {
      for (const a of images) {
        s = s.replace(new RegExp(`(${esc(a.coordinate)}:)v?${SEMVER}`, 'g'), `$1${a.tag}`);
      }
      return s;
    });
    for (const a of images) {
      for (const [found] of after.matchAll(new RegExp(`${esc(a.coordinate)}:v?${SEMVER}`, 'g'))) {
        assert(found === `${a.coordinate}:${a.tag}`, `${rel}: ${found} != ${a.coordinate}:${a.tag}`);
      }
    }
  }
}

// ---- 7. release-manifest.json: every unit -> {version, coordinate, tag, artifactKind} ----
function stable(v) {
  if (Array.isArray(v)) return v.map(stable);
  if (v && typeof v === 'object') return Object.fromEntries(Object.keys(v).sort().map((k) => [k, stable(v[k])]));
  return v;
}
{
  const unitsDir = R('release', 'units');
  const unitCoord = {};
  for (const id of readdirSync(unitsDir)) {
    const pj = JSON.parse(readFileSync(join(unitsDir, id, 'package.json'), 'utf8'));
    unitCoord[pj.pacto.releaseUnit] = pj.pacto.coordinate;
  }
  const units = {};
  for (const g of Object.values(plan.groups)) {
    for (const a of g.artifacts) {
      const tag = a.tag ?? a.release ?? a.chartVersion ?? (a.version != null ? String(a.version) : String(g.version));
      units[a.unit] = {
        version: g.version,
        coordinate: a.coordinate ?? unitCoord[a.unit] ?? null,
        tag,
        artifactKind: a.kind,
      };
    }
  }
  const manifest = stable({ schema: 'pacto-release-manifest/v1', units });
  const rel = 'release/release-manifest.json';
  const text = JSON.stringify(manifest, null, 2) + '\n';
  const p = R(rel);
  if (!existsSync(p) || readFileSync(p, 'utf8') !== text) { writeFileSync(p, text); changed.push(rel); }
}

console.log(
  `apply-release-plan: core v${core.version}, kubernetes v${k8s.version} — ` +
  (changed.length ? `updated ${changed.length} file(s):\n  ${changed.join('\n  ')}` : 'no changes (already at release state)'));
