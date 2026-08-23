---
"@pacto/core": patch
---

Keep date scalars verbatim across generic YAML round-trips.

`pacto_edit` could not edit a pristine `pacto init` scaffold. Edit reads
pacto.yaml into a `map[string]any`, and yaml.v3 resolves an unquoted
`readiness.expires: 2099-12-31` to a `time.Time`, so re-encoding wrote
`2099-12-31T00:00:00Z` and the tool rejected the contract it had just produced.
The same round-trip happens in `pkg/override` (`pacto pack --set`) and in the
structural validator, which was handing the JSON Schema layer an RFC3339 string
for a value the document spells as a bare date.

The three sites now decode through `contract.DecodeYAML`, which retags
`!!timestamp` scalars as `!!str` before decoding, so the text the author wrote
survives untouched — the same thing `contract.Parse` has always done by decoding
dates into string fields. Nothing is reformatted: a non-canonical `2099-1-1`
stays rejectable instead of being canonicalised by an unrelated edit, an explicit
`2024-01-15T00:00:00Z` keeps its time instead of being truncated to a date, and
the schema layer never checks a value that is not in the file.
