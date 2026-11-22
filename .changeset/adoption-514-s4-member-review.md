---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Review signed-in people in every member cluster with that cluster's own RBAC: `hub.url` makes a joined cluster a member whose operator dials the hub over WebSocket, proves its installation key and answers per-person list reviews with its own SubjectAccessReview, with no inbound port and no impersonate right. A member whose apiserver accepts the hub's issuer gets a token exchanged for it alone (`pacto-member-<uid>`, `--members-token-audience`); a member with another issuer is read under a person-approved group mapping (`pacto cluster map-groups`), for reads only. The hub dashboard takes `--members-host`, every hub answer lists `meta.clusters[].link` and Needs attention raises `CLUSTER_ISSUER_DIFFERS`.
