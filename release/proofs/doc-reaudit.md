# Documentation re-audit — final monorepo

Re-verification of the highest-risk documentation claims across BOTH the Pacto core
docs and the new Kubernetes integration docs, run against the FINAL assembled
monorepo site (not the prior paired-repo ledger). Every mechanical claim is checked
by `make docs-check` (`release/scripts/docs_check.py`); the consistency claims are
checked by targeted audits recorded below.

Command: `make docs-check`
Result: 9/9 checks passed.

## Automated ledger (`make docs-check`)

| # | Check | Result | Evidence |
| --- | --- | --- | --- |
| a | `docs-generate` runs from scratch | PASS | all generated pages rewritten |
| b | Generated docs match committed tree (no drift, no untracked) | PASS | `git diff --exit-code` clean on `integrations/*/docs/generated` + `docs/cli-reference.md` |
| c | `mkdocs build --strict` (no orphan pages, broken links or anchors) | PASS | strict build of the full assembled site |
| d | Every fenced Pacto contract validates with the built `pacto` CLI | PASS | 17/17 full contracts valid (section-illustrating fragments without a `service` section are not contracts and are excluded) |
| e | Every CR example validates against the generated CRD schema | PASS | 2/2 valid against `config/crd/bases/*.yaml` (offline jsonschema) |
| f | Documented controller flags match the real `--help` | PASS | 30/30 flags from `go run ./integrations/kubernetes/cmd --help` documented, none extra |
| g | Chart values + install snippets valid against the real chart | PASS | `helm lint` + `helm template` OK; every `--set` key in install/upgrade docs exists in `values.yaml` |
| h | Artifact coordinates match `release/release-manifest.json` | PASS | image/chart/module coordinates + versions present; authored install snippets use the manifest chart coordinate |
| i | Twice = no diff (deterministic generation) | PASS | second `docs-generate` produced byte-identical output |

## Consistency audits (targeted)

| Claim family | Result | Evidence |
| --- | --- | --- |
| No stale v1 contract terms | PASS | `pactoVersion: 1.x`, `pacto.dev`, `pacto.io/v1alpha1` — 0 hits across `docs/` + `integrations/kubernetes/docs/` |
| No stale status vocabulary | PASS | no `Healthy`/`Passing`/`Failing` used as a `contractStatus`; the 7 real values are `Compliant`, `Warning`, `NonCompliant`, `Reference`, `Unknown`, `Invalid`, `NotEvaluated` |
| `CONFIGURATION_ABSENT` vs `CONFIGURATION_MISMATCH` not conflated | PASS | `troubleshooting.md` states ABSENT (missing) is "distinct from" MISMATCH (exists but differs); both listed separately in the generated finding table |
| Status ladder + Unknown/Invalid/Reference/NotEvaluated consistent | PASS | `runtime-observations.md` (generated) states the precedence traced to `summarizeFindings`: Invalid outranks all, then error→NonCompliant, then unknown→Unknown, then warning→Warning, else Compliant; Reference short-circuits; NotEvaluated documented as reserved (not emitted by the operator). Consistent with `docs/` usage. |
| Fleet-% denominator not conflated with per-CR status | PASS | operator status is per-`Pacto` resource; fleet/compliance framing appears only in dashboard copy (`docs/examples/dashboard-demo.md`); the readiness numerator/denominator (`docs/contract-reference/sections.md`) excludes deferred claims from BOTH, unchanged |
| No dead links or anchors | PASS | covered by check (c), `mkdocs build --strict`; fixed the stale `#layer-4-policy-enforcement` anchor in `platform-engineers.md` (validation doc has 3 layers, anchor is `#layer-3-policy-enforcement`) |

## Traceability of generated pages

Each generated reference page is derived from a real source of truth, so it cannot
drift silently:

| Page | Source of truth |
| --- | --- |
| `crd-reference.md` | `config/crd/bases/*.yaml` (OpenAPI v3 schema) |
| `helm-reference.md` | `charts/pacto-operator/values.yaml` + `Chart.yaml` |
| `rbac.md` | `config/rbac/role.yaml` + `config/rbac/metrics-observation/servicemonitor_rbac.yaml` |
| `operator-configuration.md` | `go run ./cmd --help` (real output) + `cmd/main.go` env vars |
| `contract-bindings.md` | `Pacto` CRD `contractRef`/`target`/`overrides` fields |
| `runtime-observations.md` | `internal/observer/runtime.go` + `pkg/evidence` + `pkg/finding/codes.go` + `api/v1alpha1` enums |
| `artifact-hub.md` | `release/release-manifest.json` + `artifacthub-repo.yml` + `Chart.yaml` |
| `_compatibility.md` (snippet) | `integration.yaml` compatibility + `release/release-manifest.json` |

## Notes

- The prior 264-claim paired-repo ledger was NOT assumed valid; every claim family
  above was re-checked after moving and regenerating the docs.
- `docs/operator.md` (which linked to the now in-monorepo operator repo) was removed
  and its 6 incoming links repointed to `integrations/kubernetes/overview.md`.
- The superseded crd-ref-docs `api-reference.md` was removed in favor of the
  generated `crd-reference.md`.
