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
  (j) documented conditions + reasons match the api/v1alpha1 constants
  (k) documented events match the reasons the recorder actually emits
  (l) no bare `<placeholder>` in prose (the browser deletes it as an HTML tag)
  (m) every fenced target-state fixture actually loads as a fleet source
  (n) the signed/unsigned supply-chain table matches the real `cosign sign` sites
  (o) versions with release notes but no GitHub Release are disclosed as such
  (p) no wrapped prose line begins with a number (Markdown eats it as a list)

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

# What a real .proto file looks like. Not valid JSON and not valid YAML — which
# is the point: the validator must see the same INVALID_INTERFACE_SPEC a reader
# copying the example would get.
PROTO_STUB = 'syntax = "proto3";\n\nservice Stub {\n}\n'


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
                            # A stub has to be exactly as parseable as the real
                            # file. Every spec kind the validator supports is JSON
                            # or YAML, so "{}" stands in for all of them — except a
                            # .proto, which is protobuf IDL and which the validator
                            # rejects with INVALID_INTERFACE_SPEC. Stubbing that
                            # with "{}" would pass a doc example the real CLI fails.
                            fh.write(PROTO_STUB if ref.endswith(".proto") else "{}\n")
                    proc = run([pacto_bin, "validate", tmp])
                    if proc.returncode == 0:
                        ok += 1
                    else:
                        # The ERROR lines (with the diagnostic code) go to stderr;
                        # stdout carries only "<dir> is invalid", so preferring
                        # stdout would report a failure with no reason.
                        out = " ".join(
                            (proc.stderr.strip() or proc.stdout.strip()).split()
                        )
                        failures.append(f"{os.path.relpath(path, REPO_ROOT)}: {out}")
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
                # Compare the API GROUP, not a prefix of the whole apiVersion:
                # "pacto.trianalab.io.example/v1" starts with the same characters
                # but is a different group, and only the part before the "/" is
                # the group at all.
                group, slash, _ = str(doc.get("apiVersion", "")).partition("/")
                kind = doc.get("kind")
                if not slash or group != "pacto.trianalab.io" or kind not in schemas:
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
# (j) documented condition types + reasons  <-  the api constants
# ---------------------------------------------------------------------------

def check_conditions() -> None:
    """The troubleshooting table names Condition/Reason identifiers verbatim.

    Those are the strings an operator greps their CR status for, so a rename in
    api/v1alpha1/conditions.go silently invalidates the page. Compare the table
    against the constants rather than restating them here: nothing in this check
    hard-codes a reason, so it stays correct when a real one is added.
    """
    src = open(os.path.join(K8S, "api", "v1alpha1", "conditions.go"),
               encoding="utf-8").read()
    types = set(re.findall(r"^\tCondition\w+\s*=\s*\"(\w+)\"", src, re.M))
    reasons = set(re.findall(r"^\tReason\w+\s*=\s*\"(\w+)\"", src, re.M))

    doc_path = os.path.join(K8S, "docs", "troubleshooting.md")
    doc = open(doc_path, encoding="utf-8").read()
    rows = re.findall(r"^\| `(\w+)` \| `(?:True|False|Unknown)` \| `(\w+)` \|",
                      doc, re.M)

    problems = []
    for cond, reason in rows:
        if cond not in types:
            problems.append(f"unknown condition type `{cond}`")
        if reason not in reasons:
            problems.append(f"unknown reason `{reason}` on `{cond}`")
    for cond in sorted(types - {c for c, _ in rows}):
        problems.append(f"condition type `{cond}` is not in the table")

    ok = bool(rows) and not problems
    detail = f"{len(rows)} rows over {len(types)} condition types"
    if not rows:
        detail = "no condition rows found in troubleshooting.md"
    elif problems:
        detail = " ; ".join(problems[:5])
    record(ok, "(j) documented conditions match api constants", detail)


# ---------------------------------------------------------------------------
# (k) documented event reasons  <-  the recorder call sites
# ---------------------------------------------------------------------------

# corev1's own constants. Upstream, not ours: a local rename cannot reach them,
# and anything else in the type position is reported rather than assumed.
EVENT_TYPES = {"EventTypeNormal": "Normal", "EventTypeWarning": "Warning"}


def go_call_args(text: str, limit: int) -> list[str]:
    """Split the head of a Go argument list, honouring nesting and strings."""
    args, cur, depth, in_str, esc = [], "", 0, False, False
    for ch in text:
        if in_str:
            cur += ch
            if esc:
                esc = False
            elif ch == "\\":
                esc = True
            elif ch == '"':
                in_str = False
            continue
        if ch == '"':
            in_str = True
            cur += ch
        elif ch in "([{":
            depth += 1
            cur += ch
        elif ch in ")]}":
            if depth == 0:
                break
            depth -= 1
            cur += ch
        elif ch == "," and depth == 0:
            args.append(cur.strip())
            cur = ""
            if len(args) >= limit:
                return args
        else:
            cur += ch
    args.append(cur.strip())
    return args


def resolve_event_token(tok: str, src: str, api_consts: dict, seen=None) -> list[str]:
    """Resolve an event-type or event-reason argument to the strings it can be.

    Returns [] when the token resolves to nothing, which the caller reports.
    """
    seen = seen or set()
    if tok.startswith('"') and tok.endswith('"') and len(tok) > 1:
        return [tok[1:-1]]
    if "." in tok:                                   # pactov1alpha1.X / corev1.X
        name = tok.rsplit(".", 1)[1]
        if name in EVENT_TYPES:
            return [EVENT_TYPES[name]]
        return [api_consts[name]] if name in api_consts else []
    if not tok.isidentifier() or tok in seen:        # a local variable, once
        return []
    seen.add(tok)
    out = []
    for rhs in re.findall(rf"^\s*{re.escape(tok)}\s*(?::=|=)\s*(.+?)\s*$", src, re.M):
        out.extend(resolve_event_token(rhs, src, api_consts, seen))
    return sorted(set(out))


def check_events() -> None:
    """The troubleshooting events table names the reasons the recorder emits.

    Those are the strings an operator greps `kubectl get events` for, so a new
    call site or a renamed reason silently invalidates the page. Resolve the real
    call sites rather than restating them here: a literal is taken as-is, an
    api/v1alpha1 `EventXxx` constant is looked up, and a local variable is
    resolved through its assignments in the same file. A token that resolves to
    none of those is reported, so a new emission pattern fails this gate instead
    of slipping past it.
    """
    api_src = open(os.path.join(K8S, "api", "v1alpha1", "conditions.go"),
                   encoding="utf-8").read()
    api_consts = dict(re.findall(r"^\t(\w+)\s*=\s*\"(\w+)\"", api_src, re.M))

    problems, emitted = [], {}
    controller = os.path.join(K8S, "internal", "controller")
    for path in sorted(glob.glob(os.path.join(controller, "*.go"))):
        if path.endswith("_test.go"):
            continue
        src = open(path, encoding="utf-8").read()
        where = os.path.basename(path)
        for m in re.finditer(r"Recorder\.Eventf?\(", src):
            line_end = src.find("\n", m.end())
            args = go_call_args(src[m.end():line_end if line_end > 0 else len(src)], 3)
            if len(args) < 3 or not all(args[:3]):
                problems.append(f"{where}: cannot read the reason argument -- "
                                "put object, type and reason on the call line")
                continue
            types = resolve_event_token(args[1], src, api_consts)
            reasons = resolve_event_token(args[2], src, api_consts)
            if not types or not reasons:
                problems.append(f"{where}: unresolvable event ({args[1]}, {args[2]})")
                continue
            for reason in reasons:
                emitted.setdefault(reason, set()).update(types)

    doc_path = os.path.join(K8S, "docs", "troubleshooting.md")
    doc = open(doc_path, encoding="utf-8").read()
    rows = re.findall(r"^\| `(\w+)` \| `(Normal|Warning)` \|", doc, re.M)

    for reason, etype in rows:
        if reason not in emitted:
            problems.append(f"documented event `{reason}` is emitted nowhere")
        elif etype not in emitted[reason]:
            problems.append(f"`{reason}` is documented `{etype}`, emitted as "
                            + "/".join(sorted(emitted[reason])))
    for reason in sorted(set(emitted) - {r for r, _ in rows}):
        problems.append(f"event `{reason}` is not in the table")

    ok = bool(rows) and not problems
    detail = f"{len(rows)} rows over {len(emitted)} emitted reasons"
    if not rows:
        detail = "no event rows found in troubleshooting.md"
    elif problems:
        detail = " ; ".join(problems[:5])
    record(ok, "(k) documented events match the recorder call sites", detail)


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
    # The Helm reference is flattened to leaf keys, so a `# --` comment written on
    # a parent map is silently dropped and its children publish as blank cells.
    # Six rows shipped that way. A blank cell is a value the reader has to guess.
    ref = os.path.join(K8S, "docs", "generated", "helm-reference.md")
    undocumented = [
        m.group(1)
        for m in re.finditer(r"^\| `([\w.\[\]-]+)` \| .* \|\s*\|$", open(ref, encoding="utf-8").read(), re.M)
    ]

    ok = ok_helm and not bad_sets and not undocumented
    detail = f"helm lint+template OK, install --set keys valid, {len(known)} values all documented"
    if not ok:
        parts = []
        if not ok_helm:
            parts.append("helm lint/template failed: " + (lint.stderr or tmpl.stderr).strip()[:200])
        if bad_sets:
            parts.append("unknown --set keys: " + ", ".join(bad_sets))
        if undocumented:
            parts.append(
                f"{len(undocumented)} values.yaml keys have no `# --` description on the LEAF key: "
                + ", ".join(undocumented[:6])
            )
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
        # A pinned version written by hand goes stale on the next chart release and
        # ships a copy-pasteable command that 404s (this happened: 4.7.0 and 5.0.0).
        # The pinned commands live in generated/_{install,upgrade}-command.md, built
        # from the manifest; authored prose must --8<-- them, never inline a literal.
        for ver in re.findall(r"--version\s+(\d\S*)", text):
            problems.append(
                f"{name}: hand-written --version {ver}; include "
                f"generated/_install-command.md or _upgrade-command.md instead"
            )
    ok = not problems
    record(ok, "(h) artifact coordinates match release-manifest", "" if ok else " ; ".join(problems[:5]))


# ---------------------------------------------------------------------------
# (m) fenced target-state fixtures  <-  the real fleet source
# ---------------------------------------------------------------------------

TARGET_STATE_SCHEMA = "pacto.dev/fleet-targets/v1"


def check_target_state(pacto_bin: str, md_files: list[str]) -> None:
    """Every documented target-state fixture must actually load.

    The decoder rejects unknown fields and a wrong schemaVersion outright, and
    reports the failure as the same bare SOURCE_UNAVAILABLE a missing file gets,
    so a documented fixture with a misspelled field would look plausible on the
    page and contribute nothing when a reader ran it.
    """
    total = ok = 0
    failures = []
    empty = tempfile.mkdtemp(prefix="pacto-empty-fleet-")
    for path in md_files:
        for block in fenced_yaml_blocks(path):
            for doc in yaml_docs(block):
                if doc.get("schemaVersion") != TARGET_STATE_SCHEMA:
                    continue
                total += 1
                tmp = tempfile.mkdtemp(prefix="pacto-targets-")
                try:
                    fixture = os.path.join(tmp, "targets.yaml")
                    with open(fixture, "w", encoding="utf-8") as fh:
                        fh.write(block)
                    proc = run([pacto_bin, "fleet", "status", "--local", empty,
                                "--target-state", fixture, "--output-format", "json"])
                    try:
                        meta = json.loads(proc.stdout or "{}")
                        meta = meta.get("meta", meta)
                    except json.JSONDecodeError:
                        meta = {}
                    bad = [lim for lim in (meta.get("limitations") or [])
                           if lim.get("code") in ("SOURCE_UNAVAILABLE", "SOURCE_RECORD_INVALID")]
                    if proc.returncode == 0 and not bad:
                        ok += 1
                    else:
                        why = "; ".join(lim.get("message", "") for lim in bad) or \
                            " ".join((proc.stderr or proc.stdout).split())[:200]
                        failures.append(f"{os.path.relpath(path, REPO_ROOT)}: {why}")
                finally:
                    shutil.rmtree(tmp, ignore_errors=True)
    shutil.rmtree(empty, ignore_errors=True)
    detail = f"{ok}/{total} fenced target-state fixtures load"
    if not total:
        detail = f"no fenced `{TARGET_STATE_SCHEMA}` block found -- the format is undocumented again"
    elif failures:
        detail += " | " + " ; ".join(failures[:3])
    record(bool(total) and ok == total, "(m) fenced target-state fixtures load", detail)


# ---------------------------------------------------------------------------
# (l) no HTML-swallowed placeholders
# ---------------------------------------------------------------------------

# A bare `<name>` in prose is not text: the browser parses it as an unknown HTML
# element and renders it as nothing at all. `/helm-reference/` published the
# required signature subject as `oci://@sha256:` and `/cli-reference/` told the
# reader to write the private seed to `.key` -- both silently, both on a
# reference page a reader copies from. Inside a fence or a code span Markdown
# escapes it; in prose it does not.
PLACEHOLDER_TAG = re.compile(r"<([A-Za-z][A-Za-z0-9._-]*)>")

# Real HTML the site is allowed to use in Markdown prose. Everything else that
# looks like a tag is a placeholder the reader was supposed to see.
HTML_IN_PROSE = {
    "a", "abbr", "b", "br", "code", "details", "div", "em", "figcaption",
    "figure", "hr", "i", "img", "kbd", "li", "ol", "p", "picture", "pre", "s",
    "small", "source", "span", "strong", "sub", "summary", "sup", "svg",
    "table", "tbody", "td", "th", "thead", "tr", "u", "ul",
    "h1", "h2", "h3", "h4", "h5", "h6",
}


def unescaped_placeholders(path: str) -> list[tuple[int, str]]:
    """Every bare <placeholder> outside a fenced block and outside a code span."""
    hits = []
    in_fence = False
    for n, line in enumerate(open(path, encoding="utf-8").read().split("\n"), 1):
        stripped = line.lstrip()
        if stripped.startswith("```") or stripped.startswith("~~~"):
            in_fence = not in_fence
            continue
        if in_fence or "<" not in line:
            continue
        # Even segments are prose, odd ones are code spans.
        parts = line.split("`")
        for j in range(0, len(parts), 2):
            for m in PLACEHOLDER_TAG.finditer(parts[j]):
                if m.group(1).lower() not in HTML_IN_PROSE:
                    hits.append((n, m.group(0)))
    return hits


def check_placeholders() -> None:
    # Partials too: their bytes are spliced into a page by --8<-- and rendered
    # exactly the same way, so an unescaped placeholder there disappears just as
    # thoroughly. site_markdown() skips them because the other checks parse whole
    # documents.
    files = list(site_markdown())
    for d in integration_docs_dirs():
        files += glob.glob(os.path.join(d, "generated", "_*.md"))
    problems = []
    for p in sorted(set(files)):
        for n, tag in unescaped_placeholders(p):
            problems.append(f"{os.path.relpath(p, REPO_ROOT)}:{n}: {tag}")
    ok = not problems
    record(ok, "(l) no HTML-swallowed <placeholders> in prose",
           "" if ok else f"{len(problems)} found -- " + " ; ".join(problems[:5]))


# ---------------------------------------------------------------------------
# (n) supply-chain table  <-  the cosign sign call sites in release.yml
# ---------------------------------------------------------------------------

RELEASE_YML = os.path.join(REPO_ROOT, ".github", "workflows", "release.yml")
SUPPLY_CHAIN_DOC = os.path.join(REPO_ROOT, "docs", "installation.md")
COORD = re.compile(r"ghcr\.io/[a-z0-9._/-]+")


def signed_coordinates() -> set[str]:
    """Coordinates the release workflow actually runs `cosign sign` against.

    Signing targets are written as `${REF}` inside a job whose env sets `REF:`,
    or as a bare literal. Track the last `REF:` seen and resolve on the way past
    -- crude, but it is the same reading a human does, and it is grounded in the
    workflow rather than in a second document repeating the claim.
    """
    ref = ""
    found = set()
    # A signing command is routinely wrapped, and the coordinate is on the
    # continuation line. Collapse `\`-continuations before scanning.
    text = re.sub(r"\\\n\s*", " ", open(RELEASE_YML, encoding="utf-8").read())
    for line in text.splitlines():
        m = re.match(r"\s*REF:\s*(\S+)", line)
        if m:
            ref = m.group(1)
        if "cosign sign" in line and not line.lstrip().startswith("#"):
            target = line.replace("${REF}", ref).replace("$REF", ref)
            found.update(COORD.findall(target))
    # Drop the digest/tag suffix: the table names repositories, not releases.
    return {c.split(":")[0].split("@")[0] for c in found}


def check_supply_chain() -> None:
    """The signed/unsigned table must match what the pipeline signs.

    A signature a reader assumes exists is worse than one they know is missing,
    and this table is the only place the distinction is written down. If a
    signing step is added, removed or retargeted, the table has to move with it.
    """
    text = open(SUPPLY_CHAIN_DOC, encoding="utf-8").read()
    rows = re.findall(r"^\|\s*[`(]?(ghcr\.io/[^`\s|]+)[`)]?\s*\|([^|]*)\|\s*$", text, re.M)
    if not rows:
        record(False, "(n) supply-chain table matches the signing pipeline",
               "no ghcr.io rows found in docs/installation.md -- table moved or renamed")
        return

    real = signed_coordinates()
    claimed_signed, claimed_unsigned = set(), set()
    for coord, ships in rows:
        # A row may name a repository family (`.../<service>`); compare the stem.
        stem = coord.split("<")[0].rstrip("/")
        (claimed_signed if "cosign" in ships.lower() else claimed_unsigned).add(stem)

    problems = []
    for c in sorted(claimed_signed - real):
        problems.append(f"{c} is documented as signed but release.yml never signs it")
    for c in sorted(real - claimed_signed):
        problems.append(f"release.yml signs {c} but the table does not list it as signed")
    for c in sorted(claimed_unsigned & real):
        problems.append(f"{c} is documented as unsigned but release.yml signs it")

    ok = not problems
    record(ok, "(n) supply-chain table matches the signing pipeline",
           f"{len(real)} signed coordinates, table agrees" if ok else " ; ".join(problems[:5]))


def check_unreleased_versions() -> None:
    """A version with release notes but no GitHub Release must say so.

    Changesets writes a CHANGELOG entry when the version is bumped, not when it
    is published, so an abandoned publishing transaction leaves a version that
    reads as shipped and cannot be installed. The Changesets files are the
    historical record and must not be rewritten, so the assembled Changelog page
    carries the warning instead -- and every version it names has to be the same
    set the release post-mortem documents.
    """
    sys.path.insert(0, os.path.join(REPO_ROOT, "release", "scripts"))
    try:
        from mkdocs_integration_hook import _UNRELEASED_VERSIONS, _CHANGELOG_INTRO
    except Exception as exc:                                    # pragma: no cover
        record(False, "(o) unreleased versions are disclosed on the Changelog",
               f"cannot import the changelog assembler: {exc}")
        return

    post_mortem = open(os.path.join(REPO_ROOT, "docs", "maintainers", "releases.md"),
                       encoding="utf-8").read()
    problems = []
    for v in _UNRELEASED_VERSIONS:
        if v not in _CHANGELOG_INTRO:
            problems.append(f"{v} is listed as unreleased but the Changelog intro never names it")
        if v not in post_mortem:
            problems.append(f"{v} is listed as unreleased but docs/maintainers/releases.md never explains it")

    # The converse: a version the post-mortem calls abandoned must be disclosed.
    for v in re.findall(r"Abandoned transaction[^\n]*?\(([0-9./ ]+)\)", post_mortem):
        for ver in re.findall(r"\d+\.\d+\.\d+", v):
            if ver not in _UNRELEASED_VERSIONS:
                problems.append(f"releases.md documents {ver} as abandoned but the Changelog does not warn about it")

    ok = not problems
    record(ok, "(o) unreleased versions are disclosed on the Changelog",
           f"{len(_UNRELEASED_VERSIONS)} tagged-but-unreleased versions disclosed"
           if ok else " ; ".join(problems[:5]))


# ---------------------------------------------------------------------------
# (p) a wrapped prose line must not begin with a number
# ---------------------------------------------------------------------------

ORDERED_MARKER = re.compile(r"^(\s*)\d{1,9}[.)]\s")
# Lines that legitimately precede a list, or that are not running prose at all.
NOT_PROSE = re.compile(r"^ *(?:[-*+>|#]|\d{1,9}[.)]\s|<|:{3}|={3}|!{3}|\?{3}|---|\[.*\]:|\{)")


def accidental_ordered_lists(path: str) -> list[tuple[int, str]]:
    """Wrapped sentences that Markdown silently turns into an ordered list.

    A list is allowed to interrupt a paragraph, so a sentence that happens to
    wrap onto a line starting `1. ` is parsed as a list item: the number is
    *deleted* from the rendered page and the sentence is torn in two. Nothing
    warns -- not `mkdocs build --strict`, not a link check -- and the page still
    reads plausibly, which is what makes it worth a gate.

    The trigger is a number sitting at the *same* indent as the prose line above
    it (up to the three spaces Markdown still counts as the same block), with no
    blank line, no colon lead-in and no list marker to announce a deliberate
    list. Item 2 of a real list is safe on all three counts: it follows either
    its own more-indented continuation line or another marker. Indentation is
    compared line-to-line rather than to the left margin because the paragraph
    may itself be nested -- inside an admonition, inside a list item.
    """
    hits = []
    in_fence = False
    prev = ""
    for n, line in enumerate(open(path, encoding="utf-8").read().split("\n"), 1):
        stripped = line.lstrip()
        if stripped.startswith("```") or stripped.startswith("~~~"):
            in_fence, prev = not in_fence, ""
            continue
        if in_fence:
            continue
        m = ORDERED_MARKER.match(line)
        prev_indent = len(prev) - len(prev.lstrip())
        if (m and prev.strip()
                and prev_indent <= len(m.group(1)) <= prev_indent + 3
                and not prev.rstrip().endswith(":")
                and not NOT_PROSE.match(prev)):
            hits.append((n, line.strip()[:72]))
        prev = line
    return hits


def check_accidental_lists() -> None:
    files = list(site_markdown()) + [os.path.join(REPO_ROOT, "README.md")]
    for d in integration_docs_dirs():
        files += glob.glob(os.path.join(d, "generated", "_*.md"))
    problems = []
    for p in sorted(set(files)):
        for n, text in accidental_ordered_lists(p):
            problems.append(f"{os.path.relpath(p, REPO_ROOT)}:{n}: {text}")
    ok = not problems
    record(ok, "(p) no wrapped prose line starts with a swallowed number",
           "" if ok else f"{len(problems)} found -- " + " ; ".join(problems[:5]))


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
        check_target_state(pacto_bin, md)        # (m)

    check_flags()                                # (f)
    check_chart()                                # (g)
    check_coordinates()                          # (h)
    check_conditions()                           # (j)
    check_events()                               # (k)
    check_placeholders()                         # (l)
    check_supply_chain()                         # (n)
    check_unreleased_versions()                  # (o)
    check_accidental_lists()                     # (p)

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
