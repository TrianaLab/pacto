<!--
  GENERATED FILE -- DO NOT EDIT.
  Produced by release/scripts/gen_integration_docs.py from release/release-manifest.json.
  Regenerate with `make docs-generate`; drift is a CI failure (`make docs-check`).
-->

```bash
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/TrianaLab/pacto/integrations/kubernetes/v6.0.1/integrations/kubernetes/config/crd/bases/delivery.pacto.trianalab.io_approvals.yaml
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/TrianaLab/pacto/integrations/kubernetes/v6.0.1/integrations/kubernetes/config/crd/bases/delivery.pacto.trianalab.io_operations.yaml
kubectl apply --server-side --force-conflicts \
  -f https://raw.githubusercontent.com/TrianaLab/pacto/integrations/kubernetes/v6.0.1/integrations/kubernetes/config/crd/bases/delivery.pacto.trianalab.io_stages.yaml
```
