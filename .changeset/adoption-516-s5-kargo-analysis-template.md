---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

- `pacto impact --running oci://<repo>@<digest>` never judges a revision against itself: a target already running the new digest is listed in `running.excluded`, so a gate that runs after a deploy, such as a Kargo verification, judges the new revision against the other revisions that run. A promoter fingerprint can carry an `offer` (`id`, `text`, an `https` `doc`), which its Detected providers row lists under `offers` on `pacto fleet providers`, `pacto_fleet_status` and the dashboard's Data sources. The demo shows the Kargo row with its offer.
- The chart's default Kargo fingerprints offer "Gate every Kargo promotion with one shared AnalysisTemplate", and the GitOps docs publish that `ClusterAnalysisTemplate`: each Stage adds one `verification` stanza, exit 2 (unverified) fails unless `allow-unverified` is `"true"`, and the template reads the signed target snapshots by default and needs no cluster RBAC.
