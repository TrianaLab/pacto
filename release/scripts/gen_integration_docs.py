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
  runtime-observations.md  <- internal/observer/runtime.go + `go run ./cmd/gendocs registry`
                              (finding.Registry + pkg/evidence typed constants)
  field-coverage.md        <- pkg/validation/schema/field-coverage.yaml (the field coverage inventory)
  artifact-hub.md          <- release/release-manifest.json + artifacthub-repo.yml + Chart.yaml
  _compatibility.md        <- integration.yaml (compatibility) + release/release-manifest.json
  _install-command.md      <- release/release-manifest.json (operator-chart version)
  _upgrade-command.md      <- release/release-manifest.json (operator-chart version)
  _delivery-install-command.md <- release/release-manifest.json (operator-chart version)
  _crd-apply.md            <- release/release-manifest.json (k8s-module release tag)

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
    out = ["| API groups | Resources | Verbs | Limited to |", "| --- | --- | --- | --- |"]
    def sortkey(rule):
        return (",".join(rule.get("apiGroups", [])), ",".join(rule.get("resources", [])))
    for rule in sorted(rules, key=sortkey):
        groups = ", ".join(f"`{g or '\"\" (core)'}`" for g in rule.get("apiGroups", []))
        res = ", ".join(f"`{r}`" for r in rule.get("resources", []))
        verbs = ", ".join(f"`{v}`" for v in sorted(rule.get("verbs", [])))
        names = rule.get("resourceNames") or []
        scope = ", ".join(f"`{n}`" for n in names) if names else "*not name-restricted*"
        out.append(f"| {groups} | {res} | {verbs} | {scope} |")
    return out


def _rule_key(rule: dict) -> tuple:
    return (
        tuple(rule.get("apiGroups", [])),
        tuple(rule.get("resources", [])),
        tuple(sorted(rule.get("verbs", []))),
        tuple(rule.get("resourceNames", []) or []),
    )


def helm_rbac(k8s: str, *set_args: str) -> dict[tuple[str, str, str], dict]:
    """Render the chart and return its RBAC objects keyed by (kind, namespace, name).

    Generated from `helm template`, not from config/rbac/role.yaml: the chart is
    the only documented install path, and its ClusterRole is a different object
    from the kubebuilder-generated `manager-role` under config/. Documenting the
    latter described permissions no chart user has. The namespace is part of the
    key because the manager Role has the same name in the release namespace and
    in the node agent's.
    """
    chart = os.path.join(k8s, "charts", "pacto-operator")
    cmd = ["helm", "template", "pacto-operator", chart, "--namespace", "pacto-operator-system"]
    for s in set_args:
        cmd += ["--set", s]
    proc = subprocess.run(cmd, capture_output=True, text=True)
    if proc.returncode != 0:
        raise SystemExit(f"helm template failed: {proc.stderr.strip()[:400]}")
    out = {}
    for doc in yaml.safe_load_all(proc.stdout):
        if doc and doc.get("kind") in ("ClusterRole", "Role"):
            meta = doc["metadata"]
            out[(doc["kind"], meta.get("namespace", ""), meta["name"])] = doc
    return out


def _split(full: dict, minimal: dict, key: tuple) -> tuple[list[dict], list[dict]]:
    """The rules of one role present with every component off, and those a component adds."""
    min_keys = {_rule_key(r) for r in (minimal.get(key) or {}).get("rules", [])}
    rules = full[key]["rules"]
    return [r for r in rules if _rule_key(r) in min_keys], [r for r in rules if _rule_key(r) not in min_keys]


def gen_rbac(k8s: str) -> str:
    full = helm_rbac(k8s)
    minimal = helm_rbac(k8s, "dashboard.enabled=false", "evidence.enabled=false")
    agent = helm_rbac(k8s, "nodeAgent.enabled=true")
    metrics_docs = load_yaml_docs(
        os.path.join(k8s, "config/rbac/metrics-observation/servicemonitor_rbac.yaml")
    )

    release_ns = "pacto-operator-system"
    cr_key = next(k for k in full if k[0] == "ClusterRole")
    cr_name = cr_key[2]
    mgr_key = next(k for k in full if k[0] == "Role" and k[2].endswith("-manager"))
    le_key = next((k for k in full if k[0] == "Role" and k[2].endswith("-leader-election")), None)
    role_name = le_key[2] if le_key else None
    always, component_only = _split(full, minimal, cr_key)
    ns_always, ns_component = _split(full, minimal, mgr_key)
    agent_key = next((k for k in agent if k[0] == "Role" and k[1] != release_ns), None)
    agent_cluster = [r for r in agent[cr_key]["rules"]
                     if _rule_key(r) not in {_rule_key(x) for x in full[cr_key]["rules"]}]

    out = [banner("helm template of charts/pacto-operator (+ config/rbac/metrics-observation)")]
    out.append("# RBAC\n")
    out.append(
        "Every table below is rendered from the Helm chart itself, so it is the permission "
        "set an install actually creates. The chart creates one cluster-scoped "
        f"`ClusterRole` (`{cr_name}`) bound to the controller's ServiceAccount for what has "
        f"to be cluster-wide, a `Role` (`{mgr_key[2]}`) in the release namespace for the "
        "managed components that run there, a second one of the same name in the node "
        "agent's namespace when the agent is on, and a `Role` "
        f"(`{role_name}`) for leader election. The tables render the release namespace as "
        f"`{release_ns}`.\n"
    )
    out.append("!!! note\n")
    out.append(
        "    The repository also contains `config/rbac/role.yaml`, a kubebuilder-generated "
        "`manager-role`. It is a different object with a different name and is **not** what "
        "`helm install` creates. It belongs to the `config/` kustomize scaffolding, which is "
        "not published with a release and is deployed by no test or CI job -- Helm is the "
        "only supported install path.\n"
    )

    out.append(f"## Always granted (`{cr_name}`)\n")
    out.append(
        "Present in every install, including one with every managed component disabled. "
        "Workloads and their wiring are read-only; the writes are on Pacto's own resources, "
        "on events, and on the specific named objects a previous install may have created "
        "(so the operator can clean them up after you disable a component).\n"
    )
    out.extend(_rbac_rules_table(always))
    out.append("")

    # The unrestricted `secrets get,list,watch` row is the single grant reviewers
    # stop on, and the table alone gives them no reason for it and no way to tell
    # whether watchNamespace narrows it (it does not -- that flag changes what the
    # controller watches, not what the ClusterRole permits).
    out.append(
        "!!! warning \"Why the operator can read Secrets in every namespace\"\n\n"
        "    `spec.contractRef.pullSecretRef` names a Secret **in the Pacto's own "
        "namespace**, and a `Pacto` can be created in any namespace, so the read "
        "cannot be scoped to one. `get` resolves those registry credentials when a "
        "contract is pulled; `list` and `watch` back the Secret informer that "
        "re-reconciles a Pacto when its pull Secret changes.\n\n"
        "    **`controller.watchNamespace` does not narrow this.** It restricts what "
        "the controller reconciles; the ClusterRole is created unconditionally and "
        "grants the same cluster-wide read either way.\n\n"
        "    What does limit the blast radius is binding each credential to its host: "
        "give an Opaque pull Secret a `registry` key and the operator refuses to send "
        "it anywhere else, so a contract cannot redirect pull traffic to an "
        "attacker-controlled registry to exfiltrate the token. Beyond that, treat "
        "cluster-wide Secret read as the cost of the operator and install it on a "
        "cluster where that is acceptable.\n"
    )

    out.append(f"## Always granted in the release namespace (`{mgr_key[2]}` Role)\n")
    out.append(
        "The teardown half of the managed components: `get` and `delete` on the fixed names "
        "a previous install may have created there, the node agent's included (releases "
        "before the dedicated agent namespace ran it in the release namespace).\n"
    )
    out.extend(_rbac_rules_table(ns_always))
    out.append("")

    if component_only:
        values = load_yaml_docs(os.path.join(k8s, "charts/pacto-operator/values.yaml"))[0]
        state = {
            c: bool((values.get(c) or {}).get("enabled")) for c in ("dashboard", "evidence")
        }
        defaults_sentence = " and ".join(
            f"`{c}.enabled` is **{'on' if on else 'off'}**" for c, on in state.items()
        )
        enabled_now = [c for c, on in state.items() if on]
        out.append("## Additionally granted when a managed component is enabled\n")
        out.append(
            "When a managed component is on, the operator creates and reconciles that "
            "component's Deployment, Service, ServiceAccount and RBAC for you, and the chart "
            f"widens the ClusterRole accordingly. At chart defaults {defaults_sentence}, so "
            "every rule below is what a default install adds"
            + (f" for the {' and '.join(enabled_now)}" if enabled_now else "")
            + ". Rendering the chart with `"
            + " ".join(f"--set {c}.enabled=false" for c in state)
            + "` removes every rule in this table.\n"
        )
        out.extend(_rbac_rules_table(component_only))
        out.append("")
        if ns_component:
            out.append(
                "Their namespaced half is granted by the release namespace's "
                f"`{mgr_key[2]}` Role, so the operator can create these kinds there and "
                "nowhere else:\n"
            )
            out.extend(_rbac_rules_table(ns_component))
            out.append("")
        escalating = [
            r for r in component_only
            # Set intersection, not `in`: apiGroups is a list of exact group names,
            # and a membership test against a URL-shaped string reads to a scanner
            # as a substring check on a host. Same shape as the verbs test below.
            if {"rbac.authorization.k8s.io"} & set(r.get("apiGroups") or [])
            and not r.get("resourceNames")
            and {"create", "update", "patch"} & set(r.get("verbs", []))
        ]
        if escalating:
            out.append('!!! warning "This grant allows privilege escalation"\n')
            out.append(
                "    The rules above include unrestricted `create` on `clusterroles` and "
                "`clusterrolebindings`. A subject that can create a ClusterRoleBinding can "
                "grant itself any permission in the cluster, so at chart defaults the operator "
                "is effectively cluster-admin-capable, not read-only. This is what lets it "
                "create the managed components' RBAC.\n"
            )
            out.append(
                "    If your threat model does not allow that, install with "
                "`--set dashboard.enabled=false --set evidence.enabled=false` and deploy those "
                "components yourself. The operator then keeps only the *Always granted* table "
                "plus narrow `get`/`delete` on the specific objects a previous install may have "
                "left behind.\n"
            )

    if agent_key:
        out.append("## Additionally granted with the node agent (`nodeAgent.enabled`)\n")
        out.append(
            f"The agent runs in its own namespace (`{agent_key[1]}` by default), which the chart "
            "creates with the privileged Pod Security level. Cluster-wide, the operator gains "
            "a node read (the orphan prune), a read and relabel of that one namespace by name, "
            "and a read of the agents' heartbeat Leases (also granted when "
            "`controller.kernelEvidence.enabled` is on alone):\n"
        )
        out.extend(_rbac_rules_table(agent_cluster))
        out.append("")
        out.append(
            f"Everything it creates for the agent is granted by the `{agent_key[2]}` Role in the "
            "agent namespace. The agents' own Role (written by the operator) is `get`, `create` "
            "and `patch` on their observations and `get`, `create`, `patch` and `update` on "
            "their Leases, and Kubernetes lets the operator grant only what it holds, so it "
            "holds those verbs here:\n"
        )
        out.extend(_rbac_rules_table(agent[agent_key]["rules"]))
        out.append("")
        out.append(
            "Where the API serves `ValidatingAdmissionPolicy`, the chart also renders one that "
            "lets each agent write only the observation and Lease named after the node in its "
            "token, which RBAC cannot express.\n"
        )

    if role_name:
        out.append(f"## Namespaced Role (`{role_name}`)\n")
        out.append(
            "Created in the release namespace and bound to the same ServiceAccount. Used only "
            "for the controller-runtime leader election lease.\n"
        )
        out.extend(_rbac_rules_table(minimal[le_key]["rules"]))
        out.append("")

    metrics_role = next(
        (d for d in metrics_docs if d.get("kind") == "ClusterRole"), None
    )
    if metrics_role:
        out.append("## Optional: metrics-observation ClusterRole\n")
        out.append(
            "Needed alongside the base role when `--enable-metrics-observation` is set. It is a "
            "separate `ClusterRole` (`metrics-observation-role`), never a patch of the base role, "
            "so the base grants are untouched. The Helm chart grants the same rules itself when "
            "`controller.enableMetricsObservation` is on; apply the two objects yourself only for "
            "an operator installed without the chart. "
            "[Opt-in features](limitations.md#opt-in-features) has the YAML and the caveats.\n"
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


def chart_arg_flags(helper: str) -> dict[str, str]:
    """Map flag name -> how the chart renders it, for every controller arg the
    chart can emit, as a literal `- --flag` or a quoted `- {{ printf "--flag=...` line.

    The Deployment's args come from one helper template and there is no
    `extraArgs` value, so a flag the helper never emits is unreachable on the
    documented install path -- saying "set them via chart values" of every flag
    was simply false. Read from the template rather than hardcoded, so a new
    chart value updates the page. Three outcomes:

      "chart value" -- the arg line interpolates a value, or an enclosing
                       if/with/range guard tests one, so values.yaml decides.
      "always on"   -- the chart emits it unconditionally with a literal, so it
                       is fixed at the chart's value and no value changes it.
      (absent)      -- the helper never emits it.
    """
    body = helper[helper.index('define "pacto-operator.controllerArgs"'):]
    nxt = body.find("\n{{- define ")
    out: dict[str, str] = {}
    guards: list[str] = []
    for line in (body if nxt < 0 else body[:nxt]).splitlines():
        m = re.match(r'^\s*-\s+(?:\{\{\s*printf\s+")?--([a-z0-9-]+)', line)
        if m:
            controlled = ".Values" in line or any(".Values" in g for g in guards)
            out[m.group(1)] = "chart value" if controlled else "always on"
        opens = len(re.findall(r"\{\{-?\s*(?:if|with|range)\b", line))
        closes = len(re.findall(r"\{\{-?\s*end\b", line))
        guards.extend([line] * opens)
        for _ in range(closes):
            if guards:
                guards.pop()
    return out


def gen_operator_configuration(repo_root: str, k8s: str) -> str:
    help_text = controller_help(repo_root)
    flags = parse_flags(help_text)
    main_src = read(os.path.join(k8s, "cmd/main.go"))
    env_vars = sorted(set(re.findall(r'os\.Getenv\("([^"]+)"\)', main_src)))

    helper = read(os.path.join(k8s, "charts/pacto-operator/templates/_helpers.tpl"))
    chart_flags = chart_arg_flags(helper)

    out = [banner("`go run ./integrations/kubernetes/cmd --help` (real output) + cmd/main.go")]
    out.append("# Operator configuration\n")
    out.append(
        "Controller flags and their exact defaults are captured from the operator's real "
        "`--help` output.\n"
    )
    out.append(
        "**One dash or two makes no difference.** The table shows the single-dash spelling "
        "because that is how Go's `flag` package prints and reports flags, and other pages "
        "write `--enable-metrics-observation`. The parser accepts both forms identically: "
        "`--enable-metrics-observation=x` and `-enable-metrics-observation=x` produce the "
        "same `invalid boolean value \"x\" for -enable-metrics-observation` error. Neither "
        "spelling is more correct.\n"
    )
    out.append(
        "**Two different defaults can apply to the same flag.** The Default column below is "
        "the *binary's* default -- what you get running the controller with no arguments. The "
        "Helm chart renders its own fixed argument list, so where a chart value exists it "
        "decides, and its default may differ. `-enable-dashboard` is the one that catches "
        "people out: the binary defaults it off, the chart's `dashboard.enabled` defaults it "
        "on, and a chart install therefore runs the managed dashboard. See the "
        "[Helm reference](helm-reference.md) for the values and their defaults.\n"
    )
    out.append(
        "**A flag the chart never renders cannot be set on the documented install path.** The "
        "chart has no `extraArgs`, so the *Via chart* column is the whole story: `chart value` "
        "means some value in `values.yaml` renders this flag; `always on` means the chart "
        "hardcodes it and no value changes it; `no` means the chart never passes it, and "
        "reaching it requires patching the Deployment after install -- which `helm upgrade` "
        "then reverts. See [Limitations](limitations.md#opt-in-features).\n"
    )
    out.append("## Command-line flags\n")
    out.append("| Flag | Type | Default | Via chart | Description |")
    out.append("| --- | --- | --- | --- | --- |")
    for f in flags:
        default = f"`{f['default']}`" if f["default"] != "" else ""
        typ = f"`{f['type']}`" if f["type"] else "`bool`"
        settable = chart_flags.get(f["name"], "no")
        out.append(
            f"| `-{f['name']}` | {typ} | {default} | {settable} | {oneline(f['desc'])} |"
        )
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
    out.append(
        "## Choosing a reference form\n\n"
        "The three `contractRef.oci` forms are not three styles. They decide what the "
        "binding *means* over time, and the operator records which one it inferred in "
        "`status.resolutionPolicy` -- `PinnedDigest`, `PinnedTag` or `Latest`.\n\n"
        "- **Digest** (`...@sha256:<digest>`) is the only form whose meaning cannot "
        "change. Every reconcile re-reads the same bytes, so a compliance verdict is "
        "reproducible: what the operator asserted last Tuesday is what it asserts "
        "today. The cost is real -- publishing a contract revision becomes a change to "
        "the `Pacto` resource too.\n"
        "- **Tag** (`...:1.2.3`) is a name you control, not an immutable one. If someone "
        "force-pushes that tag the operator notices rather than drifting silently: it "
        "records a new `PactoRevision` and emits a `TagOverwritten` Warning Event naming "
        "the old and new digests. It does not refuse the new content. Choose a tag when "
        "you want a promotable pointer and will treat that Event as a signal.\n"
        "- **Unversioned** (`ghcr.io/org/service-pacto`) re-resolves the highest semver "
        "tag on every reconcile -- every `spec.checkIntervalSeconds`, 300 by default. It "
        "fits an environment whose job is to run whatever is newest, such as a staging "
        "namespace or a preview cluster. It fits production badly, because what is being "
        "asserted changes without anyone deciding to change it.\n\n"
        "With no other constraint: digest in production, unversioned in staging. The "
        "middle form is for teams that already run a tag-promotion discipline.\n\n"
        "This page covers the binding fields only. The rest of `spec` -- including "
        "`checkIntervalSeconds`, which sets how often the operator re-checks compliance "
        "(default `300`) -- is in the [CRD reference](crd-reference.md).\n"
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


def engine_registry(repo_root: str) -> dict:
    """The engine vocabularies, read through Go rather than scraped from source:
    `go run ./cmd/gendocs registry` prints the finding registry (finding.Registry)
    and pkg/evidence's typed kinds, outcomes and fact values as JSON."""
    proc = subprocess.run(
        ["go", "run", "./cmd/gendocs", "registry"],
        cwd=repo_root,
        capture_output=True,
        text=True,
        check=True,
    )
    return json.loads(proc.stdout)


def gen_runtime_observations(repo_root: str, k8s: str) -> str:
    observer_src = read(os.path.join(k8s, "internal/observer/runtime.go"))
    engine = engine_registry(repo_root)

    dims = parse_dimensions(observer_src)
    outcomes = engine["outcomes"]
    kinds = engine["observationKinds"]
    registry = [(r["code"], r["category"], r["severity"]) for r in engine["findings"]]

    # Map dimensions to the flag that gates them (keyword match on flag help text).
    gate = {
        "Health": "`--enable-probing` (Tier A active probe; passive Tier B otherwise)",
        "Metrics": "`--enable-metrics-observation`",
        "Interfaces": "`--interface-name-match-discovery` (optional positive-availability assist); "
                      "`--enable-probing` for the protocol fact",
    }
    # Dimensions the kernel merge in internal/observer/kernel.go folds node agent evidence into.
    kernel_dims = {
        "Interfaces": "listeners on the ready backing pods",
        "Dependencies": "outbound connections from the target pods",
    }

    # Collector names stamped into observation provenance and finding evidenceRefs.
    collectors = engine["collectors"]

    out = [banner("internal/observer/runtime.go, pkg/evidence, pkg/finding, pkg/runtimefacts, api/v1alpha1")]
    out.append("# Runtime observations\n")
    out.append(
        "What the operator looks at in your cluster, and every status and finding it can report. "
        "It gathers evidence (typed observations of what is running) from two sources, and the "
        "Pacto engine turns that evidence into findings and a contract status. The Kubernetes "
        "collector reads Kubernetes resources and always runs. The optional node agent watches "
        "TCP sockets in the Linux kernel with eBPF and publishes one `NodeRuntimeObservation` per "
        "node; the operator merges it into some dimensions when kernel evidence is on "
        "(`--enable-kernel-evidence`). See [Runtime conformance](runtime-conformance.md). This "
        "page is generated from the operator's observer and the shared engine packages.\n"
    )

    out.append("## Observation dimensions\n")
    out.append(
        "A dimension is one thing the operator checks, such as interfaces or dependencies. "
        "Each is observed only when the contract declares the matching section. Spec sections "
        "refer to the operator specification. A dash in the kernel column means the dimension "
        "comes from the Kubernetes API alone.\n"
    )
    out.append("| Dimension | Spec section | Gated by | Kernel evidence |")
    out.append("| --- | --- | --- | --- |")
    for d in dims:
        out.append(
            f"| {d['name']} | {d['section']} | {gate.get(d['name'], 'always on')} "
            f"| {kernel_dims.get(d['name'], '-')} |"
        )
    out.append("")
    out.append(
        "The kernel source also reports one thing no dimension asks for: a successful connection "
        "to another Pacto-managed Service that no declared dependency resolves to, which the "
        "engine reports as `DEPENDENCY_UNDECLARED`. It reports none while a declared dependency "
        "resolves to no Pacto target by ref or name (a local or other non-OCI ref, or an OCI ref no "
        "Pacto carries), because that dependency could be the peer the pod reached.\n"
    )

    out.append("## Collectors\n")
    out.append(
        "Every observation names the collector that produced it in its provenance, and every "
        "finding copies that name into `evidenceRefs[].collector`.\n"
    )
    out.append("| Collector | Meaning |")
    out.append("| --- | --- |")
    out.append(f"| `{collectors['kubernetes']}` | Kubernetes API only. |")
    out.append(f"| `{collectors['kubernetesKernel']}` | Kubernetes API result merged with node agent evidence that changed or qualified it. |")
    out.append(f"| `{collectors['kernel']}` | Node agent evidence only (an undeclared peer). |")
    out.append("")

    out.append("## Observation outcomes\n")
    out.append(
        "Every observation carries an outcome. Only `Observed` can lead to a violation; "
        "every other outcome becomes an `Unknown` finding instead.\n"
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
    order = ["Observed", "Unsupported", "Failed", "Stale", "Insufficient"]
    for name in order + sorted(set(outcomes) - set(order)):
        if name in outcomes:
            out.append(f"| `{name}` | {outcome_desc.get(name, '')} |")
    out.append("")

    out.append("## Assertion kinds\n")
    out.append("An assertion is one claim of the contract the operator can check. These are the "
               "kinds of evidence it emits, one per assertion:\n")
    for k in sorted(kinds):
        out.append(f"- `{k}`")
    out.append("")

    out.append("## Facts\n")
    out.append(
        "Details an observation can carry beyond \"it is there\", such as the protocol a port "
        "answers with. An empty fact means not observed: the engine lists the check in "
        "`evaluationCoverage.unverified` and never treats it as a contradiction. Values are "
        "generated from `pkg/evidence/facts.go`.\n"
    )
    out.append("| Fact | Value | Meaning |")
    out.append("| --- | --- | --- |")
    for typ in ("Protocol", "Exposure"):
        for fact in engine["facts"][typ]:
            out.append(f"| `{typ}` | `{fact['value']}` | {oneline(fact['meaning'])} |")
    out.append("")

    out.append("## Contract status\n")
    out.append(
        "`status.contractStatus` says whether the service matches its contract. It is not a "
        "health check. Values are generated from the `Pacto` API.\n"
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
        "A finding is one conclusion from the engine. Findings fall in two families. Confirmed "
        "violations (`RuntimeDrift`, `error`) need evidence that clearly contradicts the "
        "contract. Uncertainty findings (`Inconclusive`, `unknown`) mean the evidence could "
        "neither confirm nor refute it. "
        "A code listed under two severities is an error when the claim it reports on is "
        "`required` and a warning when it is optional, because an optional claim cannot make a "
        "contract non-compliant. "
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
# Field coverage  <-  pkg/validation/schema/field-coverage.yaml
# ---------------------------------------------------------------------------

RUNTIME_CLASSES = [
    ("verified", "A runtime fact can both satisfy and contradict the declaration."),
    ("partial", "A runtime fact exists, but some values or environments stay unverified."),
    ("static-only", "Checked by `pacto validate` only; nothing at runtime reads it."),
    ("not-applicable", "Identity, container or open object with nothing to verify."),
]


def _coverage_tests(tests: dict | None) -> str:
    """Render {unit: [...], integration: [...], e2e: [...]} as one table cell."""
    parts = []
    for kind in ("unit", "integration", "e2e"):
        refs = (tests or {}).get(kind) or []
        if refs:
            parts.append(f"{kind}: " + ", ".join(f"`{r}`" for r in refs))
    return "; ".join(parts) or "none"


PLATFORMS = [
    ("kubernetes", "Kubernetes", "the operator with its node agent"),
    ("agent", "`pacto agent`", "a Docker host or a server"),
    ("run", "`pacto run`", "one wrapped command"),
]


def _platform_cell(f: dict, key: str) -> str:
    p = (f.get("platforms") or {}).get(key)
    return f"`{p['evidence']}`" if p else "-"


def gen_field_coverage(repo_root: str) -> str:
    inv = load_yaml_docs(os.path.join(repo_root, "pkg/validation/schema/field-coverage.yaml"))[0]
    fields = inv["fields"]  # YAML order is the schema order; keep it
    out = [banner("pkg/validation/schema/field-coverage.yaml")]
    out.append("# Field coverage\n")
    out.append(
        "For every field you can write in a contract, this page says what checks it: "
        "`pacto validate` alone, a runtime check against the running service or nothing. "
        "Use it to answer \"if I declare this, will Pacto notice when reality disagrees?\" "
        "Each field also names the tests that prove its rule.\n"
    )
    out.append(
        "[Runtime conformance](../../runtime-conformance.md) runs three ways, and they do not "
        "see the same things: the Kubernetes operator also reads the cluster API, while "
        "`pacto agent` and `pacto run` see only the host. Every runtime-checked field says, "
        "per way, whether its evidence is `verified`, `partial` or `unverified` and why.\n"
    )
    out.append(
        "The page is generated from `pkg/validation/schema/field-coverage.yaml`. "
        "`pkg/validation/fieldcoverage_test.go` fails when a schema path or enum value is missing "
        "from that file, when an entry names no schema path and when a referenced test does "
        "not exist. Paths use `[]` for array items.\n"
    )
    out.append("## Runtime classes\n")
    out.append("| Class | Meaning | Fields |")
    out.append("| --- | --- | --- |")
    for cls, desc in RUNTIME_CLASSES:
        n = sum(1 for f in fields.values() if f.get("runtime") == cls)
        out.append(f"| `{cls}` | {desc} | {n} |")
    out.append("")
    out.append(
        "A runtime check never turns \"could not look\" into a violation. A check with no "
        "usable fact is listed in `status.evaluationCoverage.unverified` and leaves the "
        "contract status unchanged.\n"
    )
    out.append("## Summary\n")
    out.append("| Field | Runtime | " + " | ".join(label for _, label, _ in PLATFORMS) + " | Outcomes |")
    out.append("| --- | --- | " + " | ".join("---" for _ in PLATFORMS) + " | --- |")
    for path, f in fields.items():
        cells = " | ".join(_platform_cell(f, key) for key, _, _ in PLATFORMS)
        out.append(f"| `{path}` | `{f.get('runtime', '')}` | {cells} | {oneline(f.get('outcomes'))} |")
    out.append("")
    out.append("## Fields\n")
    for path, f in fields.items():
        out.append(f"### `{path}`\n")
        out.append("| Key | Value |")
        out.append("| --- | --- |")
        for key in ("meaning", "runtime", "evidence", "rule", "outcomes", "limitations"):
            val = oneline(str(f.get(key, "")))
            if key == "runtime":
                val = f"`{val}`"
            out.append(f"| {key.capitalize()} | {val} |")
        out.append(f"| Tests | {_coverage_tests(f.get('tests'))} |")
        out.append("")
        platforms = f.get("platforms") or {}
        if platforms:
            out.append("| Platform | Evidence | Why | Tests |")
            out.append("| --- | --- | --- | --- |")
            for key, label, where in PLATFORMS:
                p = platforms[key]
                tests = ", ".join(f"`{t}`" for t in p.get("tests") or []) or (
                    "the e2e scenarios above" if key == "kubernetes" else "-")
                out.append(f"| {label} ({where}) | `{p['evidence']}` | {oneline(p['reason'])} | {tests} |")
            out.append("")
        values = f.get("values") or {}
        if values:
            out.append("| Value | Runtime | Meaning | Rule | Outcomes | Limitations | Tests |")
            out.append("| --- | --- | --- | --- | --- | --- | --- |")
            for val, v in values.items():
                cells = [oneline(str(v.get(k, ""))) or "-" for k in ("meaning", "rule", "outcomes", "limitations")]
                out.append(
                    f"| `{val}` | `{v.get('runtime', '')}` | " + " | ".join(cells)
                    + f" | {_coverage_tests(v.get('tests'))} |"
                )
            out.append("")
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Published artifacts  <-  release-manifest.json + artifacthub-repo.yml + Chart.yaml
# ---------------------------------------------------------------------------

def gen_artifact_hub(repo_root: str, k8s: str) -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    units = manifest["units"]
    ah = load_yaml_docs(os.path.join(k8s, "artifacthub-repo.yml"))[0]
    chart = load_yaml_docs(os.path.join(k8s, "charts/pacto-operator/Chart.yaml"))[0]

    out = [banner("release/release-manifest.json + artifacthub-repo.yml + Chart.yaml")]
    out.append("# Published artifacts\n")
    out.append(
        "Published artifact coordinates for the Kubernetes integration. All coordinates and "
        "versions are generated from `release/release-manifest.json` -- the single source of "
        "truth for what has been published.\n"
    )
    out.append("## Artifact coordinates\n")
    out.append("| Artifact | Kind | Coordinate | Version |")
    out.append("| --- | --- | --- | --- |")
    order = ["operator-image", "operator-chart", "delivery-image", "k8s-module", "k8s-docs"]
    labels = {
        "operator-image": "Controller image",
        "operator-chart": "Helm chart",
        "delivery-image": "Delivery image (opt-in)",
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
    # The image and the chart are the two artifacts the release workflow signs,
    # so the coordinates table is the one place a reader is guaranteed to be
    # looking at them. Emit the verify command from the manifest rather than a
    # literal, for the same reason `--version` is generated.
    signed = [units[u] for u in ("operator-image", "operator-chart") if u in units]
    if signed:
        out.append("## Verify a published artifact\n")
        out.append(
            "The controller image and the Helm chart, and the opt-in delivery image and "
            "chart, are signed keylessly by the release workflow through GitHub's OIDC "
            "issuer. Verify each one before installing it:\n"
        )
        out.append("```bash")
        out.append("cosign verify \\")
        out.append(
            "  --certificate-identity-regexp "
            "'^https://github\\.com/TrianaLab/pacto/\\.github/workflows/release\\.yml@' \\"
        )
        out.append("  --certificate-oidc-issuer https://token.actions.githubusercontent.com \\")
        out.append(f"  {signed[0]['coordinate']}:{signed[0]['version']}")
        out.append("```")
        out.append("")
        out.append(
            "Anything other than a successful verification -- including `no signatures "
            "found` -- means do not deploy it. Not every Pacto artifact is signed; see "
            "[what is signed and what is not]"
            "(../../installation.md#supply-chain-what-is-signed-and-what-is-not).\n"
        )
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

def chart_command(chart_unit: dict, verb: str, release: str = "pacto-operator",
                  namespace: str = "pacto-operator-system", extra: str = "") -> str:
    """Fenced `helm install|upgrade` block pinned to the published chart version."""
    if verb == "install":
        extra = " --create-namespace" + extra
    return (
        "```bash\n"
        f"helm {verb} {release} \\\n"
        f"  oci://{chart_unit['coordinate']} \\\n"
        f"  --version {chart_unit['version']} \\\n"
        f"  --namespace {namespace}{extra}\n"
        "```\n"
    )


def gen_chart_command(repo_root: str, verb: str) -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    return banner("release/release-manifest.json") + chart_command(
        manifest["units"]["operator-chart"], verb
    )


# Delivery is a section of the operator chart: turning it on is an upgrade of the
# operator release that keeps its values. A later `helm upgrade` updates the
# delivery CRDs too, since they are templates, so no CRD snippet goes with it.
def gen_delivery_chart_command(repo_root: str) -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    return banner("release/release-manifest.json") + chart_command(
        manifest["units"]["operator-chart"], "upgrade",
        extra=" \\\n  --reuse-values --set delivery.enabled=true",
    )


# The CRD apply in upgrade.md used to fetch from `main`, one command above a
# `helm upgrade` pinned to an exact chart version: the reader was told to install
# chart X and to apply whatever CRDs happened to be on the default branch that
# day. Same drift class as the `--version` literal above, so same fix -- pin the
# ref to the integration release tag the docs describe. The file list is read
# from config/crd/bases, so a new CRD cannot be left out of the upgrade snippet
# (`helm upgrade` never adds or updates CRDs). Only the operator's own group: the
# delivery.pacto.trianalab.io CRDs are chart templates behind delivery.enabled,
# which helm upgrade installs and updates itself.
CRD_BASES = "integrations/kubernetes/config/crd/bases"
RAW_BASE = "https://raw.githubusercontent.com/TrianaLab/pacto"


def gen_crd_apply(repo_root: str, group: str = "pacto.trianalab.io") -> str:
    manifest = json.loads(read(os.path.join(repo_root, "release/release-manifest.json")))
    tag = manifest["units"]["k8s-module"]["tag"]
    paths = sorted(glob.glob(os.path.join(repo_root, CRD_BASES, f"{group}_*.yaml")))
    if not paths:
        raise SystemExit(f"no {group} CRDs in {CRD_BASES}")
    out = [banner("release/release-manifest.json"), "```bash"]
    for path in paths:
        out.append("kubectl apply --server-side --force-conflicts \\")
        out.append(f"  -f {RAW_BASE}/{tag}/{CRD_BASES}/{os.path.basename(path)}")
    out.append("```")
    return "\n".join(out) + "\n"


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
    out.append(f"| Delivery image | `{units['delivery-image']['version']}` | `{pacto_core_req}` |")
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
        "shows the integration version those docs describe. Pick a core version from the "
        "selector to read the integration docs that shipped with it.\n"
    )
    return "\n".join(out).rstrip() + "\n"


# ---------------------------------------------------------------------------
# Driver
# ---------------------------------------------------------------------------
# Placeholder escaping
# ---------------------------------------------------------------------------

# A bare `<name>` is read by the browser as an unknown HTML element and rendered
# as nothing at all. The sources these pages are generated from are full of them
# -- `oci://<repo>@sha256:<digest>` in a chart annotation, `<namespace>` in a
# flag's help text -- and inside a fenced block or a code span Markdown escapes
# them for us. In prose it does not, so `/helm-reference/` published the required
# signature subject as the meaningless string `oci://@sha256:`.
#
# Escaping happens here, on the generated page, and never in the upstream source:
# the same strings are printed to a terminal by `--help` and read by Helm, where
# `&lt;repo&gt;` would be the wrong thing to show.
_PLACEHOLDER_TAG = re.compile(r"<([A-Za-z][A-Za-z0-9._-]*)>")


def escape_placeholders(md: str) -> str:
    """Escape every bare `<placeholder>` outside fenced blocks and code spans."""
    lines = md.split("\n")
    in_fence = False
    for i, line in enumerate(lines):
        stripped = line.lstrip()
        if stripped.startswith("```") or stripped.startswith("~~~"):
            in_fence = not in_fence
            continue
        if in_fence or "<" not in line:
            continue
        # Split on backticks: even segments are prose, odd ones are code spans.
        # An unbalanced backtick leaves the tail treated as code, which errs
        # towards changing nothing.
        parts = line.split("`")
        for j in range(0, len(parts), 2):
            parts[j] = _PLACEHOLDER_TAG.sub(r"&lt;\1&gt;", parts[j])
        lines[i] = "`".join(parts)
    return "\n".join(lines)


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
        "field-coverage.md": gen_field_coverage(repo_root),
        "artifact-hub.md": gen_artifact_hub(repo_root, k8s),
        "_compatibility.md": gen_compatibility(repo_root, k8s),
        "_install-command.md": gen_chart_command(repo_root, "install"),
        "_upgrade-command.md": gen_chart_command(repo_root, "upgrade"),
        "_delivery-install-command.md": gen_delivery_chart_command(repo_root),
        "_crd-apply.md": gen_crd_apply(repo_root),
    }
    for name, content in pages.items():
        path = os.path.join(out_dir, name)
        # Last step, over every finished page, so no generator has to remember.
        with open(path, "w", encoding="utf-8") as fh:
            fh.write(escape_placeholders(content))
        print(f"wrote {os.path.relpath(path, repo_root)}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
