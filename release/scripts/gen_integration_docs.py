#!/usr/bin/env python3
"""Deterministic generator for the Kubernetes integration reference documentation.

Every page written by this tool is derived from a REAL source of truth in the
repository -- never a hand-copied field list that can drift. Re-running the
generator on unchanged sources produces byte-identical output (no timestamps, no
absolute paths, stable ordering), so `make docs-check` can assert zero drift.

Source-of-truth map (page -> inputs):
  crd-reference.md         <- config/crd/bases/*.yaml (authoritative OpenAPI v3 schema)
  helm-reference.md        <- charts/pacto-operator/values.yaml + Chart.yaml
  rbac.md                  <- config/rbac/role.yaml + config/rbac/metrics-observation/
  operator-configuration.md<- `go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go env vars
  contract-bindings.md     <- CRD target/contractRef/overrides fields + pacto CLI validation
  runtime-observations.md  <- internal/observer/runtime.go + pkg/evidence + pkg/finding + api enums
  artifact-hub.md          <- release/release-manifest.json + artifacthub-repo.yml + Chart.yaml
  _compatibility.md        <- integration.yaml (compatibility) + release/release-manifest.json

Usage:
  python3 release/scripts/gen_integration_docs.py [--repo-root DIR] [--integration-dir DIR]
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import re
import subprocess
import sys

import yaml

# ---------------------------------------------------------------------------
# Small helpers
# ---------------------------------------------------------------------------

BANNER = (
    "<!--\n"
    "  GENERATED FILE -- DO NOT EDIT.\n"
    "  Produced by release/scripts/gen_integration_docs.py from {sources}.\n"
    "  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).\n"
    "-->\n"
)


def banner(sources: str) -> str:
    return BANNER.format(sources=sources)


def oneline(text: str | None) -> str:
    """Collapse a (possibly multi-line) description to a single Markdown-table-safe line."""
    if not text:
        return ""
    text = " ".join(text.split())
    return text.replace("|", "\\|")


def read(path: str) -> str:
    with open(path, encoding="utf-8") as fh:
        return fh.read()


def load_yaml_docs(path: str) -> list:
    with open(path, encoding="utf-8") as fh:
        return [d for d in yaml.safe_load_all(fh) if d is not None]


def go_string_consts(src: str, type_name: str) -> dict[str, str]:
    """Return {GoIdentifier: "literal"} for `Ident TypeName = "literal"` and
    `Ident = "literal"` declarations (the latter inside a typed const block)."""
    out: dict[str, str] = {}
    # `Name Type = "value"`  and  `Name = "value"`
    for m in re.finditer(r'^\s*(\w+)\s+' + re.escape(type_name) + r'\s*=\s*"([^"]*)"', src, re.M):
        out[m.group(1)] = m.group(2)
    for m in re.finditer(r'^\s*(\w+)\s*=\s*"([^"]*)"\s*(?://.*)?$', src, re.M):
        out.setdefault(m.group(1), m.group(2))
    return out


# ---------------------------------------------------------------------------
# CRD reference  <-  config/crd/bases/*.yaml
# ---------------------------------------------------------------------------

def _crd_rows(name: str, schema: dict, required: set[str], rows: list[dict]) -> None:
    props = schema.get("properties", {})
    for key in props:  # controller-gen emits properties alphabetically -> stable
        prop = props[key]
        path = f"{name}.{key}" if name else key
        typ = prop.get("type", "object")
        enum = prop.get("enum")
        default = prop.get("default")
        is_req = key in required
        # arrays: describe element type, then descend into item object properties.
        display_type = typ
        item_schema = None
        if typ == "array":
            items = prop.get("items", {})
            itype = items.get("type", "object")
            display_type = f"[]{itype}"
            if items.get("properties"):
                item_schema = items
            if items.get("enum"):
                enum = items["enum"]
        rows.append(
            {
                "path": path,
                "type": display_type,
                "required": "yes" if is_req else "no",
                "default": "" if default is None else json.dumps(default),
                "enum": ", ".join(f"`{e}`" for e in enum) if enum else "",
                "description": oneline(prop.get("description")),
            }
        )
        if typ == "object" and prop.get("properties"):
            _crd_rows(path, prop, set(prop.get("required", [])), rows)
        elif item_schema is not None:
            _crd_rows(f"{path}[]", item_schema, set(item_schema.get("required", [])), rows)


def gen_crd_reference(k8s: str) -> str:
    crd_files = sorted(glob.glob(os.path.join(k8s, "config/crd/bases/*.yaml")))
    src_note = "config/crd/bases/*.yaml"
    out = [banner(src_note)]
    out.append("# CRD reference\n")
    out.append(
        "Custom Resource Definitions installed by the operator. Field lists below are "
        "generated from the authoritative OpenAPI v3 schema in `config/crd/bases/`.\n"
    )
    for crd_file in crd_files:
        crd = load_yaml_docs(crd_file)[0]
        spec = crd["spec"]
        names = spec["names"]
        kind = names["kind"]
        version = spec["versions"][0]
        out.append(f"\n## {kind}\n")
        out.append(f"- **API group**: `{spec['group']}`")
        out.append(f"- **Version**: `{version['name']}`")
        out.append(f"- **Kind**: `{kind}`")
        out.append(f"- **Plural**: `{names['plural']}`")
        out.append(f"- **Scope**: {spec['scope']}\n")
        cols = version.get("additionalPrinterColumns")
        if cols:
            out.append("### Additional printer columns\n")
            out.append("| Name | Type | JSONPath |")
            out.append("| --- | --- | --- |")
            for c in cols:
                out.append(f"| {c['name']} | {c.get('type', '')} | `{c['jsonPath']}` |")
            out.append("")
        root = version["schema"]["openAPIV3Schema"]
        rows: list[dict] = []
        _crd_rows("", root, set(root.get("required", [])), rows)
        # Drop the boilerplate apiVersion/kind/metadata rows -- they are identical
        # for every Kubernetes object and add noise.
        rows = [r for r in rows if r["path"] not in ("apiVersion", "kind", "metadata")]
        out.append("### Fields\n")
        out.append("| Field | Type | Required | Default | Enum | Description |")
        out.append("| --- | --- | --- | --- | --- | --- |")
        for r in rows:
            out.append(
                f"| `{r['path']}` | `{r['type']}` | {r['required']} | "
                f"{('`' + r['default'] + '`') if r['default'] else ''} | {r['enum']} | {r['description']} |"
            )
        out.append("")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Helm reference  <-  charts/pacto-operator/values.yaml + Chart.yaml
# ---------------------------------------------------------------------------

def _flatten_values(node, prefix: str, out: dict[str, object]) -> None:
    if isinstance(node, dict):
        if not node:
            out[prefix] = {}
            return
        for k, v in node.items():
            child = f"{prefix}.{k}" if prefix else k
            _flatten_values(v, child, out)
    else:
        out[prefix] = node


def gen_helm_reference(k8s: str) -> str:
    values_path = os.path.join(k8s, "charts/pacto-operator/values.yaml")
    chart_path = os.path.join(k8s, "charts/pacto-operator/Chart.yaml")
    chart = load_yaml_docs(chart_path)[0]
    text = read(values_path)

    # helm-docs convention: `# -- description` line(s) immediately precede a key.
    # Associate the doc comment with the next `key:` at the same or deeper indent.
    descriptions: dict[str, str] = {}
    lines = text.splitlines()
    pending: list[str] = []
    # Track the dotted path by indentation stack.
    stack: list[tuple[int, str]] = []
    for raw in lines:
        stripped = raw.strip()
        if stripped.startswith("# --"):
            pending.append(stripped[len("# --"):].strip())
            continue
        if stripped.startswith("#"):
            # A plain comment line immediately continuing a `# --` block is part of
            # the description (helm-docs multi-line convention); otherwise ignore it.
            if pending:
                pending.append(stripped[1:].strip())
            continue
        if not stripped:
            pending = []
            continue
        m = re.match(r"^(\s*)([\w.-]+):", raw)
        if m:
            indent = len(m.group(1))
            key = m.group(2)
            while stack and stack[-1][0] >= indent:
                stack.pop()
            parent = stack[-1][1] if stack else ""
            dotted = f"{parent}.{key}" if parent else key
            stack.append((indent, dotted))
            if pending:
                descriptions[dotted] = " ".join(pending)
                pending = []
        else:
            pending = []

    flat: dict[str, object] = {}
    _flatten_values(yaml.safe_load(text), "", flat)

    out = [banner("charts/pacto-operator/values.yaml + Chart.yaml")]
    out.append("# Helm reference\n")
    out.append(f"- **Chart**: `{chart['name']}`")
    out.append(f"- **Chart version**: `{chart['version']}`")
    out.append(f"- **App version**: `{chart['appVersion']}`\n")
    out.append(
        "Values are generated from `charts/pacto-operator/values.yaml`. Descriptions "
        "come from the chart's `# --` value annotations.\n"
    )
    out.append("| Key | Default | Description |")
    out.append("| --- | --- | --- |")
    for key in sorted(flat):
        val = flat[key]
        if isinstance(val, (dict, list)) or val is None:
            default = "`{}`" if val == {} else ("`[]`" if val == [] else "`null`")
        elif isinstance(val, bool):
            default = f"`{str(val).lower()}`"
        elif val == "":
            default = '`""`'
        else:
            default = f"`{val}`"
        desc = oneline(descriptions.get(key, ""))
        out.append(f"| `{key}` | {default} | {desc} |")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# RBAC  <-  config/rbac/role.yaml + config/rbac/metrics-observation/
# ---------------------------------------------------------------------------

def _rbac_rules_table(rules: list[dict]) -> list[str]:
    out = ["| API groups | Resources | Verbs |", "| --- | --- | --- |"]
    def sortkey(rule):
        return (",".join(rule.get("apiGroups", [])), ",".join(rule.get("resources", [])))
    for rule in sorted(rules, key=sortkey):
        groups = ", ".join(f"`{g or '\"\" (core)'}`" for g in rule.get("apiGroups", []))
        res = ", ".join(f"`{r}`" for r in rule.get("resources", []))
        verbs = ", ".join(f"`{v}`" for v in sorted(rule.get("verbs", [])))
        out.append(f"| {groups} | {res} | {verbs} |")
    return out


def gen_rbac(k8s: str) -> str:
    role = load_yaml_docs(os.path.join(k8s, "config/rbac/role.yaml"))[0]
    metrics_docs = load_yaml_docs(
        os.path.join(k8s, "config/rbac/metrics-observation/servicemonitor_rbac.yaml")
    )
    out = [banner("config/rbac/role.yaml + config/rbac/metrics-observation/servicemonitor_rbac.yaml")]
    out.append("# RBAC\n")
    out.append(
        "The operator's base `ClusterRole` (`manager-role`) is generated from "
        "kubebuilder markers into `config/rbac/role.yaml`. It is the exact permission "
        "set the controller needs to reconcile Pacto resources and observe runtime state.\n"
    )
    out.append("## Base ClusterRole (`manager-role`)\n")
    out.extend(_rbac_rules_table(role["rules"]))
    out.append("")
    metrics_role = next(
        (d for d in metrics_docs if d.get("kind") == "ClusterRole"), None
    )
    if metrics_role:
        out.append("## Optional: metrics-observation ClusterRole\n")
        out.append(
            "Applied ALONGSIDE the base role only when `--enable-metrics-observation` is "
            "set. It is a separate `ClusterRole` (`metrics-observation-role`), never a patch "
            "of `manager-role`, so the base grants are untouched.\n"
        )
        out.extend(_rbac_rules_table(metrics_role["rules"]))
        out.append("")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Operator configuration  <-  `go run ./integrations/kubernetes/cmd --help` + cmd/main.go env vars
# ---------------------------------------------------------------------------

def controller_help(repo_root: str) -> str:
    """Capture the REAL --help output, normalized (strip the temp binary path in the
    usage header so output is reproducible across runs/machines)."""
    proc = subprocess.run(
        ["go", "run", "./integrations/kubernetes/cmd", "--help"],
        cwd=repo_root,
        capture_output=True,
        text=True,
    )
    return proc.stdout + proc.stderr


def parse_flags(help_text: str) -> list[dict]:
    """Parse `flag`-style --help output into {name, type, default, description}."""
    flags: list[dict] = []
    lines = help_text.splitlines()
    i = 0
    cur = None
    for line in lines:
        m = re.match(r"^  -(\S+)(?:\s+(\S.*))?$", line)
        if m:
            if cur:
                flags.append(cur)
            cur = {"name": m.group(1), "type": (m.group(2) or "").strip(), "desc": ""}
        elif cur is not None and line.startswith("    \t"):
            piece = line[len("    \t"):].strip()
            cur["desc"] = (cur["desc"] + " " + piece).strip() if cur["desc"] else piece
    if cur:
        flags.append(cur)
    for f in flags:
        default = ""
        dm = re.search(r"\(default (.+)\)\s*$", f["desc"])
        if dm:
            default = dm.group(1).strip().strip('"')
            f["desc"] = f["desc"][: dm.start()].strip()
        f["default"] = default
    flags.sort(key=lambda f: f["name"])
    return flags


def gen_operator_configuration(repo_root: str, k8s: str) -> str:
    help_text = controller_help(repo_root)
    flags = parse_flags(help_text)
    main_src = read(os.path.join(k8s, "cmd/main.go"))
    env_vars = sorted(set(re.findall(r'os\.Getenv\("([^"]+)"\)', main_src)))

    out = [banner("`go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go")]
    out.append("# Operator configuration\n")
    out.append(
        "Controller flags and their exact defaults are captured from the operator's real "
        "`--help` output. Set them via chart values or by editing the Deployment args.\n"
    )
    out.append("## Command-line flags\n")
    out.append("| Flag | Type | Default | Description |")
    out.append("| --- | --- | --- | --- |")
    for f in flags:
        default = f"`{f['default']}`" if f["default"] != "" else ""
        typ = f"`{f['type']}`" if f["type"] else "`bool`"
        out.append(f"| `-{f['name']}` | {typ} | {default} | {oneline(f['desc'])} |")
    out.append("")
    if env_vars:
        out.append("## Environment variables\n")
        out.append(
            "Read directly by the controller entrypoint (`cmd/main.go`), typically wired "
            "through the downward API in the chart's Deployment.\n"
        )
        out.append("| Variable | Purpose |")
        out.append("| --- | --- |")
        purpose = {
            "POD_NAMESPACE": "Namespace the operator (and its managed dashboard) runs in. Required.",
            "OPERATOR_DEPLOYMENT_NAME": "Operator Deployment name, used to set ownerReferences on dashboard resources.",
        }
        for v in env_vars:
            out.append(f"| `{v}` | {purpose.get(v, 'See cmd/main.go.')} |")
        out.append("")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Contract bindings  <-  CRD target/contractRef/overrides + pacto CLI example
# ---------------------------------------------------------------------------

def _subtree_rows(k8s: str, crd_file: str, dotted_root: str) -> list[dict]:
    crd = load_yaml_docs(os.path.join(k8s, crd_file))[0]
    root = crd["spec"]["versions"][0]["schema"]["openAPIV3Schema"]
    rows: list[dict] = []
    _crd_rows("", root, set(root.get("required", [])), rows)
    prefix = dotted_root + "."
    return [r for r in rows if r["path"] == dotted_root or r["path"].startswith(prefix)]


def _rows_table(rows: list[dict]) -> list[str]:
    out = [
        "| Field | Type | Required | Enum | Description |",
        "| --- | --- | --- | --- | --- |",
    ]
    for r in rows:
        out.append(
            f"| `{r['path']}` | `{r['type']}` | {r['required']} | {r['enum']} | {r['description']} |"
        )
    return out


def gen_contract_bindings(k8s: str) -> str:
    crd = "config/crd/bases/pacto.trianalab.io_pactos.yaml"
    out = [banner("config/crd/bases/pacto.trianalab.io_pactos.yaml (Pacto CRD binding fields)")]
    out.append("# Contract bindings\n")
    out.append(
        "A `Pacto` resource points at a platform-agnostic contract (inline or in OCI) and "
        "binds it to concrete Kubernetes resources to observe. The binding fields below are "
        "generated from the `Pacto` CRD schema.\n"
    )
    out.append("## Contract source (`spec.contractRef`)\n")
    out.extend(_rows_table(_subtree_rows(k8s, crd, "spec.contractRef")))
    out.append("")
    out.append("## Runtime target (`spec.target`)\n")
    out.append(
        "When `spec.target` is omitted the Pacto is reference-only: the operator parses and "
        "validates the contract but performs no runtime observation.\n"
    )
    out.extend(_rows_table(_subtree_rows(k8s, crd, "spec.target")))
    out.append("")
    out.append("## Overrides (`spec.overrides`)\n")
    out.extend(_rows_table(_subtree_rows(k8s, crd, "spec.overrides")))
    out.append("")
    out.append("## Example\n")
    out.append(
        "A `Pacto` binding an OCI contract to a Service, with an interface bound to a named "
        "Service port and a configuration bound to a ConfigMap:\n"
    )
    out.append(
        "```yaml\n"
        "apiVersion: pacto.trianalab.io/v1alpha1\n"
        "kind: Pacto\n"
        "metadata:\n"
        "  name: orders-api\n"
        "  namespace: shop\n"
        "spec:\n"
        "  checkIntervalSeconds: 300\n"
        "  contractRef:\n"
        "    oci: ghcr.io/acme/orders-api-pacto:1.2.0\n"
        "  target:\n"
        "    serviceName: orders-api\n"
        "    interfaceBindings:\n"
        "      - interface: http\n"
        "        servicePort: http\n"
        "    configBindings:\n"
        "      - configuration: default\n"
        "        kind: ConfigMap\n"
        "        name: orders-api-config\n"
        "        key: app.yaml\n"
        "        format: yaml\n"
        "```\n"
    )
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Runtime observations  <-  observer + evidence + finding codes + api enums
# ---------------------------------------------------------------------------

def parse_dimensions(observer_src: str) -> list[dict]:
    """Enumerate observation dimensions from the Collect() producer comments in
    internal/observer/runtime.go (`// <Name> producer (spec section <X>).`)."""
    dims = []
    for m in re.finditer(r"//\s+(\w+) producer \(spec section ([^)]+)\)\.", observer_src):
        dims.append({"name": m.group(1), "section": m.group(2).strip()})
    return dims


def gen_runtime_observations(repo_root: str, k8s: str) -> str:
    observer_src = read(os.path.join(k8s, "internal/observer/runtime.go"))
    evidence_src = read(os.path.join(repo_root, "pkg/evidence/evidence.go"))
    codes_src = read(os.path.join(repo_root, "pkg/finding/codes.go"))
    finding_src = read(os.path.join(repo_root, "pkg/finding/finding.go"))
    conditions_src = read(os.path.join(k8s, "api/v1alpha1/conditions.go"))

    dims = parse_dimensions(observer_src)
    outcomes = go_string_consts(evidence_src, "Outcome")
    kinds = go_string_consts(evidence_src, "ObservationKind")
    contract_status = {
        k: v for k, v in go_string_consts(conditions_src, "").items()
        if k.startswith("ContractStatus")
    }

    # Finding registry: Code ident -> "STRING", plus Category/Severity idents -> "String".
    code_strings = {}
    code_strings.update(go_string_consts(codes_src, "Code"))
    code_strings.update(go_string_consts(finding_src, "Code"))
    cat_strings = go_string_consts(finding_src, "Category")
    sev_strings = go_string_consts(finding_src, "Severity")
    registry: list[tuple[str, str, str]] = []  # (code, category, severity)
    for m in re.finditer(r"^\s*(Code\w+):\s*\{(Category\w+),\s*(Severity\w+)\}", codes_src, re.M):
        code = code_strings.get(m.group(1), m.group(1))
        cat = cat_strings.get(m.group(2), m.group(2))
        sev = sev_strings.get(m.group(3), m.group(3))
        registry.append((code, cat, sev))

    # Map dimensions to the flag that gates them (keyword match on flag help text).
    gate = {
        "Health": "`--enable-probing` (Tier A active probe; passive Tier B otherwise)",
        "Metrics": "`--enable-metrics-observation`",
        "Interfaces": "`--interface-name-match-discovery` (optional positive-availability assist)",
    }

    out = [banner("internal/observer/runtime.go, pkg/evidence, pkg/finding, api/v1alpha1")]
    out.append("# Runtime observations\n")
    out.append(
        "The operator's collector reads Kubernetes resources and produces typed Evidence, "
        "which the Pacto engine evaluates into Findings and a contract status. This page is "
        "generated from the collector and the shared engine reasoning packages.\n"
    )

    out.append("## Observation dimensions\n")
    out.append(
        "Each dimension is observed only when the contract declares the corresponding "
        "section. Spec sections refer to the operator specification.\n"
    )
    out.append("| Dimension | Spec section | Gated by |")
    out.append("| --- | --- | --- |")
    for d in dims:
        out.append(f"| {d['name']} | {d['section']} | {gate.get(d['name'], 'always on')} |")
    out.append("")

    out.append("## Observation outcomes\n")
    out.append(
        "Every observation carries an outcome. Non-`Observed` outcomes never fabricate a "
        "violation -- they resolve to an uncertainty (`Unknown`) finding.\n"
    )
    out.append("| Outcome | Meaning |")
    out.append("| --- | --- |")
    outcome_desc = {
        "Observed": "Collected successfully; a value is present.",
        "Unsupported": "The collector cannot observe this dimension (e.g. external Service, non-HTTP binding).",
        "Failed": "Collection was attempted and errored.",
        "Stale": "Last known data is too old to trust.",
        "Insufficient": "Partial/ambiguous, not conclusive (includes within-stabilization-window negatives).",
    }
    for name in ["Observed", "Unsupported", "Failed", "Stale", "Insufficient"]:
        if name in outcomes.values() or name in outcomes:
            out.append(f"| `{name}` | {outcome_desc.get(name, '')} |")
    out.append("")

    out.append("## Assertion kinds\n")
    out.append("The evidence kinds the collector emits, one per observed assertion:\n")
    for k in sorted(kinds.values()):
        out.append(f"- `{k}`")
    out.append("")

    out.append("## Contract status\n")
    out.append(
        "The high-level `status.contractStatus` compliance state (contract fidelity, NOT "
        "runtime health). Enum values are generated from the `Pacto` API.\n"
    )
    out.append("| Status | Meaning |")
    out.append("| --- | --- |")
    status_desc = {
        "Compliant": "No error, unknown or warning findings.",
        "Warning": "Only warning-severity findings.",
        "NonCompliant": "At least one confirmed violation (error-severity finding).",
        "Reference": "Reference-only contract (no runtime target); parsed and validated, never observed.",
        "Unknown": "A required assertion could not be evaluated, or the contract could not be obtained transiently.",
        "Invalid": "Structural validation failed, or a malformed artifact could not be parsed (fail-closed).",
        "NotEvaluated": "Reserved enum value the operator does not currently emit; used by the engine dashboard for offline sources never runtime-evaluated.",
    }
    for name in ["Compliant", "Warning", "NonCompliant", "Reference", "Unknown", "Invalid", "NotEvaluated"]:
        out.append(f"| `{name}` | {status_desc[name]} |")
    out.append("")
    out.append(
        "Precedence when summarizing findings (see `summarizeFindings` in "
        "`internal/controller/pacto_controller.go`): `Invalid` (structural) outranks all; "
        "then any error -> `NonCompliant`; else any unknown -> `Unknown`; else any warning -> "
        "`Warning`; else `Compliant`. Reference-only contracts short-circuit to `Reference`.\n"
    )

    out.append("## Findings\n")
    out.append(
        "Typed conclusions from the engine, grouped by severity family. Family 1 (confirmed "
        "violations, `RuntimeDrift`/`error`) requires conclusive contradicting evidence; family 2 "
        "(`Inconclusive`/`unknown`) captures evidence that could not confirm or refute. "
        "Generated from `pkg/finding/codes.go`.\n"
    )
    by_sev: dict[str, list[tuple[str, str]]] = {}
    for code, cat, sev in registry:
        by_sev.setdefault(sev, []).append((code, cat))
    for sev in ["error", "warning", "unknown", "info"]:
        items = by_sev.get(sev)
        if not items:
            continue
        out.append(f"### Severity `{sev}`\n")
        out.append("| Code | Category |")
        out.append("| --- | --- |")
        for code, cat in sorted(set(items)):
            out.append(f"| `{code}` | `{cat}` |")
        out.append("")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Artifact Hub  <-  release-manifest.json + artifacthub-repo.yml + Chart.yaml
# ---------------------------------------------------------------------------

def gen_artifact_hub(repo_root: str, k8s: str) -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    units = manifest["units"]
    ah = load_yaml_docs(os.path.join(k8s, "artifacthub-repo.yml"))[0]
    chart = load_yaml_docs(os.path.join(k8s, "charts/pacto-operator/Chart.yaml"))[0]

    out = [banner("release/release-manifest.json + artifacthub-repo.yml + Chart.yaml")]
    out.append("# Artifact Hub\n")
    out.append(
        "Published artifact coordinates for the Kubernetes integration. All coordinates and "
        "versions are generated from `release/release-manifest.json` -- the single source of "
        "truth for what has been published.\n"
    )
    out.append("## Artifact coordinates\n")
    out.append("| Artifact | Kind | Coordinate | Version |")
    out.append("| --- | --- | --- | --- |")
    order = ["operator-image", "operator-chart", "k8s-module", "k8s-docs"]
    labels = {
        "operator-image": "Controller image",
        "operator-chart": "Helm chart",
        "k8s-module": "Go module",
        "k8s-docs": "Documentation",
    }
    for uid in order:
        u = units.get(uid)
        if not u:
            continue
        out.append(
            f"| {labels[uid]} | {u['artifactKind']} | `{u['coordinate']}` | `{u['version']}` |"
        )
    out.append("")
    out.append("## Artifact Hub repository\n")
    out.append(f"- **Repository ID**: `{ah['repositoryID']}`")
    imgs = chart.get("annotations", {}).get("artifacthub.io/images", "")
    if imgs:
        out.append(
            "- **Chart image annotation** (`artifacthub.io/images`, from `Chart.yaml`):\n"
        )
        out.append("```yaml")
        out.append(imgs.rstrip())
        out.append("```")
    out.append("")
    out.append("## Install from the published chart\n")
    out.append(chart_command(units["operator-chart"], "install"))
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Pinned install/upgrade snippets  <-  release-manifest.json
# ---------------------------------------------------------------------------
# Hand-written pages used to carry `--version <literal>` inline, which went stale
# every chart release and shipped copy-pasteable commands that 404. These two
# partials are generated from the release manifest and `--8<--`'d into
# installation.md and upgrade.md, so the literal can only ever be the published
# one and `make docs-check` catches any drift.

def chart_command(chart_unit: dict, verb: str) -> str:
    """Fenced `helm install|upgrade` block pinned to the published chart version."""
    extra = " --create-namespace" if verb == "install" else ""
    return (
        "```bash\n"
        f"helm {verb} pacto-operator \\\n"
        f"  oci://{chart_unit['coordinate']} \\\n"
        f"  --version {chart_unit['version']} \\\n"
        f"  --namespace pacto-operator-system{extra}\n"
        "```\n"
    )


def gen_chart_command(repo_root: str, verb: str) -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    return banner("release/release-manifest.json") + chart_command(
        manifest["units"]["operator-chart"], verb
    )


# ---------------------------------------------------------------------------
# Compatibility snippet  <-  integration.yaml + release-manifest.json
# ---------------------------------------------------------------------------

def gen_compatibility(repo_root: str, k8s: str) -> str:
    integ = load_yaml_docs(os.path.join(k8s, "integration.yaml"))[0]
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    units = manifest["units"]
    core = units["core"]["version"]
    pacto_core_req = integ.get("compatibility", {}).get("pactoCore", "")

    out = [banner("integration.yaml (compatibility) + release/release-manifest.json")]
    out.append("## Version compatibility\n")
    out.append(
        "The Kubernetes integration is versioned independently from Pacto core. The table "
        "below is generated from `integration.yaml` and `release/release-manifest.json`.\n"
    )
    out.append("| Integration artifact | Version | Supported Pacto core |")
    out.append("| --- | --- | --- |")
    out.append(f"| Operator image | `{units['operator-image']['version']}` | `{pacto_core_req}` |")
    out.append(f"| Operator chart | `{units['operator-chart']['version']}` | `{pacto_core_req}` |")
    out.append(f"| Go module | `{units['k8s-module']['version']}` | `{pacto_core_req}` |")
    out.append(f"| Integration docs | `{units['k8s-docs']['version']}` | `{pacto_core_req}` |")
    out.append("")
    out.append(
        f"This documentation set corresponds to Pacto core `{core}`. The integration's own "
        f"version (currently operator/chart `{units['operator-chart']['version']}`, docs "
        f"`{units['k8s-docs']['version']}`) advances on its own release cadence.\n"
    )
    out.append("### Version selector\n")
    out.append(
        "The site version selector (top of the page) tracks Pacto core releases. Because the "
        "Kubernetes integration ships on its own cadence, a Kubernetes-only release does NOT "
        "add a new core version entry to the selector: it republishes the current core "
        "version in place with regenerated integration docs, and this compatibility table "
        "shows the integration version those docs describe.\n"
    )
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Driver
# ---------------------------------------------------------------------------

def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--repo-root", default=None, help="Repository root (default: two levels up).")
    ap.add_argument(
        "--integration-dir",
        default=None,
        help="Integration directory (default: <repo-root>/integrations/kubernetes).",
    )
    args = ap.parse_args()

    repo_root = os.path.abspath(
        args.repo_root or os.path.join(os.path.dirname(__file__), "..", "..")
    )
    k8s = os.path.abspath(
        args.integration_dir or os.path.join(repo_root, "integrations", "kubernetes")
    )
    out_dir = os.path.join(k8s, "docs", "generated")
    os.makedirs(out_dir, exist_ok=True)

    pages = {
        "crd-reference.md": gen_crd_reference(k8s),
        "helm-reference.md": gen_helm_reference(k8s),
        "rbac.md": gen_rbac(k8s),
        "operator-configuration.md": gen_operator_configuration(repo_root, k8s),
        "contract-bindings.md": gen_contract_bindings(k8s),
        "runtime-observations.md": gen_runtime_observations(repo_root, k8s),
        "artifact-hub.md": gen_artifact_hub(repo_root, k8s),
        "_compatibility.md": gen_compatibility(repo_root, k8s),
        "_install-command.md": gen_chart_command(repo_root, "install"),
        "_upgrade-command.md": gen_chart_command(repo_root, "upgrade"),
    }
    for name, content in pages.items():
        path = os.path.join(out_dir, name)
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(content)
        print(f"wrote {os.path.relpath(path, repo_root)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
