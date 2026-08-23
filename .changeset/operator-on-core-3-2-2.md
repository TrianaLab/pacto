---
"@pacto/k8s-module": patch
---

Rebuild the operator on pacto core v3.2.2.

The 3.2.2 Version PR bumped `integrations/kubernetes/go.mod` to
`github.com/trianalab/pacto/v3 v3.2.2`, but a core-line release does not
republish the kubernetes line, so the operator image and chart stayed on the
code built against v3.2.1. This changeset moves the kubernetes fixed group to
5.2.2 so the published operator actually carries:

- one `FILE_NOT_FOUND` finding per missing interface spec file, and a directory
  at a spec path no longer passing as a file
- date-like scalars kept verbatim through generic YAML round-trips, so an
  unquoted timestamp is validated as the text the contract author wrote
