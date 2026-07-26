#!/usr/bin/env python3
"""Comprehensive documentation gate for the Pacto monorepo (`make docs-check`).

Runs, in order, and fails on the first hard error while collecting a pass/fail
ledger for every claim family:

  (a) regenerate every generated doc from scratch (`make docs-generate`)
  (b) drift: generated docs must match the committed tree (no diff, no untracked)
  (c) `mkdocs build --strict` (no orphan pages, broken links or anchors)
  (d) every fenced Pacto contract validates with the built `pacto` CLI
  (e) every CR example validates against the generated CRD schema (offline)
  (f) documented controller flags match the real `--help`
  (g) chart values + install snippets are valid against the real Helm chart
  (h) artifact coordinates match release/release-manifest.json
  (i) twice = no diff: a second `docs-generate` produces byte-identical output

Exit code is non-zero if any check fails.
"""
from __future__ import annotations

import glob
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile

import yaml
from jsonschema import Draft4Validator

REPO_ROOT = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
K8S = os.path.join(REPO_ROOT, "integrations", "kubernetes")

ledger: list[tuple[bool, str, str]] = []


def record(ok: bool, name: str, detail: str = "") -> None:
    ledger.append((ok, name, detail))
    mark = "PASS" if ok else "FAIL"
    print(f"[{mark}] {name}" + (f" -- {detail}" if detail else ""))


def run(cmd: list[str], cwd: str = REPO_ROOT, check: bool = False) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, check=check)


# ---------------------------------------------------------------------------
# Discovery of site Markdown + generated paths
# ---------------------------------------------------------------------------

def integration_docs_dirs() -> list[str]:
    out = []
    for m in sorted(glob.glob(os.path.join(REPO_ROOT, "integrations", "*", "integration.yaml"))):
        with open(m, encoding="utf-8") as fh:
            man = yaml.safe_load(fh) or {}
        src = (man.get("documentation") or {}).get("source")
        if src and os.path.isdir(os.path.join(REPO_ROOT, src)):
            out.append(os.path.join(REPO_ROOT, src))
    return out


def generated_paths() -> list[str]:
    paths = [os.path.join("docs", "cli-reference.md")]
    for d in integration_docs_dirs():
        rel = os.path.relpath(os.path.join(d, "generated"), REPO_ROOT)
        paths.append(rel)
    return paths


def site_markdown() -> list[str]:
    files = []
    for p in glob.glob(os.path.join(REPO_ROOT, "docs", "**", "*.md"), recursive=True):
        if os.sep + "superpowers" + os.sep in p:
            continue
        files.append(p)
    for d in integration_docs_dirs():
        for p in glob.glob(os.path.join(d, "*.md")) + glob.glob(os.path.join(d, "generated", "*.md")):
            if not os.path.basename(p).startswith("_"):
                files.append(p)
    return sorted(set(files))


FENCE_RE = re.compile(r"^([ \t]*)(?:```|~~~)ya?ml[^\n]*\n(.*?)^\1(?:```|~~~)", re.M | re.S)


def fenced_yaml_blocks(path: str) -> list[str]:
    text = open(path, encoding="utf-8").read()
    blocks = []
    for m in FENCE_RE.finditer(text):
        indent, body = m.group(1), m.group(2)
        if indent:  # de-indent nested fences
            body = "\n".join(line[len(indent):] if line.startswith(indent) else line
                             for line in body.splitlines())
        blocks.append(body)
    return blocks


def yaml_docs(block: str) -> list:
    try:
        return [d for d in yaml.safe_load_all(block) if isinstance(d, dict)]
    except yaml.YAMLError:
        return []


# ---------------------------------------------------------------------------
# (d) fenced Pacto contracts  <-  pacto CLI
# ---------------------------------------------------------------------------

def is_local_ref(val) -> bool:
    return (
        isinstance(val, str)
        and val
        and not val.startswith(("oci://", "http://", "https://", "/"))
        and ("/" in val or val.endswith((".json", ".yaml", ".yml", ".proto")))
    )


def collect_local_refs(obj, out: set[str]) -> None:
    if isinstance(obj, dict):
        for k, v in obj.items():
            if k in ("ref", "schema") and is_local_ref(v):
                out.add(v)
            else:
                collect_local_refs(v, out)
    elif isinstance(obj, list):
        for v in obj:
            collect_local_refs(v, out)


def check_contracts(pacto_bin: str, md_files: list[str]) -> None:
    total = ok = 0
    failures = []
    for path in md_files:
        for block in fenced_yaml_blocks(path):
            docs = yaml_docs(block)
            for doc in docs:
                # A full contract has both pactoVersion and the (required) service
                # section. Blocks with pactoVersion but no service are section-
                # illustrating fragments (e.g. a lone readiness/metadata example),
                # not standalone contracts, so they are not validated as one.
                if "pactoVersion" not in doc or "service" not in doc:
                    continue
                total += 1
                tmp = tempfile.mkdtemp(prefix="pacto-contract-")
                try:
                    with open(os.path.join(tmp, "pacto.yaml"), "w", encoding="utf-8") as fh:
                        yaml.safe_dump(doc, fh, sort_keys=False)
                    refs: set[str] = set()
                    collect_local_refs(doc, refs)
                    for ref in refs:
                        dest = os.path.join(tmp, ref)
                        os.makedirs(os.path.dirname(dest), exist_ok=True)
                        with open(dest, "w", encoding="utf-8") as fh:
                            fh.write("{}\n")  # universal parse-only stub
                    proc = run([pacto_bin, "validate", tmp])
                    if proc.returncode == 0:
                        ok += 1
                    else:
                        failures.append(
                            f"{os.path.relpath(path, REPO_ROOT)}: "
                            f"{proc.stdout.strip() or proc.stderr.strip()}"
                        )
                finally:
                    shutil.rmtree(tmp, ignore_errors=True)
    detail = f"{ok}/{total} fenced contracts valid"
    if failures:
        detail += " | " + " ; ".join(failures[:5])
    record(ok == total, "(d) fenced Pacto contracts validate", detail)


# ---------------------------------------------------------------------------
# (e) CR examples  <-  generated CRD schema (offline)
# ---------------------------------------------------------------------------

def crd_schemas() -> dict[str, dict]:
    schemas = {}
    for f in sorted(glob.glob(os.path.join(K8S, "config/crd/bases/*.yaml"))):
        with open(f, encoding="utf-8") as fh:
            crd = yaml.safe_load(fh)
        kind = crd["spec"]["names"]["kind"]
        schemas[kind] = crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]
    return schemas


def check_cr_examples(md_files: list[str]) -> None:
    schemas = crd_schemas()
    total = ok = 0
    failures = []
    for path in md_files:
        for block in fenced_yaml_blocks(path):
            for doc in yaml_docs(block):
                api = str(doc.get("apiVersion", ""))
                kind = doc.get("kind")
                if not api.startswith("pacto.trianalab.io/") or kind not in schemas:
                    continue
                total += 1
                errors = sorted(Draft4Validator(schemas[kind]).iter_errors(doc),
                                key=lambda e: e.path)
                if not errors:
                    ok += 1
                else:
                    failures.append(
                        f"{os.path.relpath(path, REPO_ROOT)} ({kind}): {errors[0].message}"
                    )
    detail = f"{ok}/{total} CR examples valid against generated CRD"
    if failures:
        detail += " | " + " ; ".join(failures[:5])
    record(ok == total, "(e) CR examples validate against CRD", detail)


# ---------------------------------------------------------------------------
# (f) controller flags  <-  real --help
# ---------------------------------------------------------------------------

def check_flags() -> None:
    proc = run(["go", "run", "./integrations/kubernetes/cmd", "--help"])
    help_text = proc.stdout + proc.stderr
    real = set(re.findall(r"^  -(\S+)", help_text, re.M))
    gen = os.path.join(K8S, "docs", "generated", "operator-configuration.md")
    doc = open(gen, encoding="utf-8").read()
    documented = set(re.findall(r"\| `-(\S+)` \|", doc))
    missing = real - documented
    extra = documented - real
    ok = not missing and not extra
    detail = f"{len(real)} flags in --help, all documented"
    if not ok:
        detail = f"missing={sorted(missing)} extra={sorted(extra)}"
    record(ok, "(f) controller flags match --help", detail)


# ---------------------------------------------------------------------------
# (g) chart values + install snippets  <-  real Helm chart
# ---------------------------------------------------------------------------

def flatten_keys(node, prefix="", out=None):
    if out is None:
        out = set()
    if isinstance(node, dict):
        for k, v in node.items():
            child = f"{prefix}.{k}" if prefix else k
            out.add(child)
            flatten_keys(v, child, out)
    return out


def check_chart() -> None:
    chart = os.path.join(K8S, "charts", "pacto-operator")
    lint = run(["helm", "lint", chart])
    tmpl = run(["helm", "template", "pacto-operator", chart])
    ok_helm = lint.returncode == 0 and tmpl.returncode == 0

    with open(os.path.join(chart, "values.yaml"), encoding="utf-8") as fh:
        values = yaml.safe_load(fh)
    known = flatten_keys(values)
    bad_sets = []
    for path in [os.path.join(K8S, "docs", "installation.md"),
                 os.path.join(K8S, "docs", "upgrade.md")]:
        if not os.path.exists(path):
            continue
        text = open(path, encoding="utf-8").read()
        for key in re.findall(r"--set\s+([\w.]+)=", text):
            if key not in known:
                bad_sets.append(f"{os.path.basename(path)}:{key}")
    ok = ok_helm and not bad_sets
    detail = "helm lint+template OK, install --set keys valid"
    if not ok:
        parts = []
        if not ok_helm:
            parts.append("helm lint/template failed: " + (lint.stderr or tmpl.stderr).strip()[:200])
        if bad_sets:
            parts.append("unknown --set keys: " + ", ".join(bad_sets))
        detail = " | ".join(parts)
    record(ok, "(g) chart values + install snippets valid", detail)


# ---------------------------------------------------------------------------
# (h) artifact coordinates  <-  release-manifest.json
# ---------------------------------------------------------------------------

def check_coordinates() -> None:
    manifest = json.loads(open(os.path.join(REPO_ROOT, "release/release-manifest.json")).read())
    units = manifest["units"]
    ah = open(os.path.join(K8S, "docs", "generated", "artifact-hub.md"), encoding="utf-8").read()
    problems = []
    for uid in ("operator-image", "operator-chart", "k8s-module"):
        u = units[uid]
        if u["coordinate"] not in ah:
            problems.append(f"{uid} coordinate {u['coordinate']} absent from artifact-hub.md")
        if u["version"] not in ah:
            problems.append(f"{uid} version {u['version']} absent from artifact-hub.md")
    # Authored install snippets must use the manifest chart coordinate, not a stale one.
    chart_coord = units["operator-chart"]["coordinate"]
    for name in ("installation.md", "upgrade.md"):
        text = open(os.path.join(K8S, "docs", name), encoding="utf-8").read()
        for coord in re.findall(r"oci://(ghcr\.io/\S*charts/\S+)", text):
            if coord.rstrip("\\").strip() != chart_coord:
                problems.append(f"{name}: chart coordinate {coord} != manifest {chart_coord}")
    ok = not problems
    record(ok, "(h) artifact coordinates match release-manifest", "" if ok else " ; ".join(problems[:5]))


# ---------------------------------------------------------------------------
# Drift helpers
# ---------------------------------------------------------------------------

def snapshot(paths: list[str]) -> dict[str, str]:
    out = {}
    for rel in paths:
        full = os.path.join(REPO_ROOT, rel)
        for f in ([full] if os.path.isfile(full) else glob.glob(os.path.join(full, "**", "*"), recursive=True)):
            if os.path.isfile(f):
                out[os.path.relpath(f, REPO_ROOT)] = hashlib.sha256(open(f, "rb").read()).hexdigest()
    return out


def git_dirty(paths: list[str]) -> str:
    diff = run(["git", "diff", "--exit-code", "--"] + paths)
    others = run(["git", "ls-files", "--others", "--exclude-standard", "--"] + paths)
    problems = []
    if diff.returncode != 0:
        problems.append("modified: " + diff.stdout.strip().splitlines()[0] if diff.stdout else "modified")
    if others.stdout.strip():
        problems.append("untracked: " + others.stdout.strip().replace("\n", ", "))
    return " ; ".join(problems)


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main() -> int:
    gen_paths = generated_paths()

    # (a) regenerate from scratch
    proc = run(["make", "docs-generate"])
    record(proc.returncode == 0, "(a) docs-generate runs",
           "" if proc.returncode == 0 else proc.stderr.strip()[-300:])
    if proc.returncode != 0:
        return finish()

    snap1 = snapshot(gen_paths)

    # (b) drift vs committed tree
    dirty = git_dirty(gen_paths)
    record(not dirty, "(b) generated docs match committed tree (no drift)", dirty)

    # (c) strict build
    with tempfile.TemporaryDirectory() as site:
        proc = run(["mkdocs", "build", "--strict", "--site-dir", site])
    record(proc.returncode == 0, "(c) mkdocs build --strict",
           "" if proc.returncode == 0 else (proc.stderr.strip() or proc.stdout.strip())[-400:])

    # build the pacto CLI once for (d)
    pacto_bin = os.path.join(tempfile.mkdtemp(prefix="pacto-cli-"), "pacto")
    b = run(["go", "build", "-o", pacto_bin, "./cmd/pacto"])
    if b.returncode != 0:
        record(False, "build pacto CLI", b.stderr.strip()[-300:])
    else:
        md = site_markdown()
        check_contracts(pacto_bin, md)          # (d)
        check_cr_examples(md)                    # (e)

    check_flags()                                # (f)
    check_chart()                                # (g)
    check_coordinates()                          # (h)

    # (i) twice = no diff
    proc = run(["make", "docs-generate"])
    snap2 = snapshot(gen_paths)
    changed = sorted(k for k in set(snap1) | set(snap2) if snap1.get(k) != snap2.get(k))
    record(proc.returncode == 0 and not changed,
           "(i) twice = no diff (deterministic)",
           "" if not changed else "changed: " + ", ".join(changed[:5]))

    return finish()


def finish() -> int:
    failed = [n for ok, n, _ in ledger if not ok]
    passed = sum(1 for ok, _, _ in ledger if ok)
    print("\n" + "=" * 60)
    print(f"docs-check: {passed}/{len(ledger)} checks passed")
    if failed:
        print("FAILED: " + "; ".join(failed))
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
