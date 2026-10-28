**Exit code:** `1` when the overall classification is `BREAKING`, and also when the diff could not be produced. A non-zero exit alone is therefore no contract verdict. See [Exit codes](#exit-codes) for how to tell the two apart. A `POTENTIAL_BREAKING` result exits `0`, so a CI gate that stops on it must read the classification.

The diff engine performs deep comparison of referenced OpenAPI specs, detecting changes at the path, method, parameter, request body and response level. The optional `docs/` directory is ignored: documentation changes produce no diff entries and leave the compatibility classification unchanged.

When both bundles include an `sbom/` directory with recognized SBOM files (`.spdx.json` or `.cdx.json`), `pacto diff` reports package-level changes: added, removed or modified packages (version and license). SBOM changes are informational and leave the overall classification and exit code unchanged.

When dependencies change between the old and new contracts (version upgrades, additions or removals), a dependency graph diff section is displayed showing the tree of affected nodes.

See [Change Classification](contract-reference/diff.md#change-classification-rules) for the full rules.
