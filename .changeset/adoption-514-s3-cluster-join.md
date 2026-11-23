---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Join a cluster to a hub through the registry with one approval. The operator's target snapshot gains a `join` block (display name and its source, inferred environments and the installation key), and a hub lists every cluster that publishes to `<registry>/pacto-targets` with `pacto cluster list`, the MCP tool `pacto_cluster_list` and `GET /api/fleet/clusters`. `pacto cluster approve <uid> --key sha256:<key id>` and `pacto cluster remove <uid>` record a keyless decision signed as you; approving needs `approve` on `clusterjoins.pacto.trianalab.io` granted to your user and `delivery.identities.signers` on the hub. An approved member's services, targets and workloads join the hub's fleet from its next build, verified under the approved key; a removed member leaves it, and a member with a new key reads pending again. A hub dashboard reading a private registry needs a pull-only Secret in `dashboard.ociSecrets`.
