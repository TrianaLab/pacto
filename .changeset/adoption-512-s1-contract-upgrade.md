---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

pacto validate and pacto_check report `CONTRACT_BEHIND`, an info finding that
never fails a run, when a contract declares an older pactoVersion. Its message
says whether raising the version keeps what the contract means, names the
finding the raise would add or says Pacto cannot tell until the errors are
fixed. pacto_check adds it as a suggestion.

pacto_edit raises pactoVersion only when that keeps the contract's meaning and
otherwise fails with the finding the raise would add. Its change line now
names the old version: `set pactoVersion to "2.4" (was "2.3")`, comes first
and replaces the `set pactoVersion "2.1" for the external dependency` line. An
edit that needs a raise now fails when the contract already fails validation,
does not parse or declares an unknown or 1.x pactoVersion, where it used to
overwrite the version.

The fleet revision detail gains `pactoVersionCurrent` and
`pactoVersionUnknown`, and `pacto fleet entity revision`, the TUI and the
dashboard revision page show a behind or unknown version.

The operator reads its `ContractOverrides` rule for ref configurations from
validation, with no behaviour change, and its finding reference lists
`CONTRACT_BEHIND`.
