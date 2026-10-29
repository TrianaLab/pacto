The `--readiness` gate is **opt-in** because it is time-dependent: it compares the assessment's single `readiness.expires` date against the run time, which would make plain `validate` non-deterministic. Expiry is declared once for the whole assessment. Individual claims carry no `expires` field, and adding one fails to load with `PARSE_ERROR`. Without the flag, validation checks the contract's structure and rules. It still checks the shape of readiness claims and skips the freshness gate. See the [readiness reference](contract-reference/readiness.md#readiness) for the score and gate semantics.

A contract with a mistyped value fails like this:

```
svc is invalid
PASS load
SKIP lock        no pacto.lock
FAIL structural
  svc/pacto.yaml:6:1: ERROR [SCHEMA_VIOLATION] workload: at '/workload': value must be one of 'service', 'job', 'scheduled' (did you mean "service"?)
SKIP cross-field structural failed
SKIP policy      structural failed
SKIP guarantees  no dependency declares requires
SKIP readiness   not requested (--readiness)
SKIP closure     not requested (--closure)
Next: fix workload at svc/pacto.yaml:6:1 [SCHEMA_VIOLATION], then run pacto validate svc
```

Errors within a stage are listed in file order. An unknown field or an enum value close to a known one carries a `did you mean` hint. A failed `lock` stage names the `pacto lock` command that repairs it. When every stage passes, `Next` is `pacto impact PUBLISHED_REF <dir>`, which shows the services the change breaks; replace `PUBLISHED_REF` with the `oci://` reference of the version you run today. In JSON, `stages` lists each stage with its `status`, `reason` and `errors`, each error carries `line` and `column` when known (never with `--set` or `-f`, which validate a merged document whose lines differ from the file), `file` is the contract file they point into, and `next` holds the command. `errors` and `warnings` still list every stage's findings.

**Exit code:** Non-zero if validation fails.
