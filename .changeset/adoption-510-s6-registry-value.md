---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

One chart value, `registry`, turns on the registry-backed features. The operator publishes the cluster's signed target snapshot with its installation key, advertises the registry, snapshot, index and inferred refs, and pushes only under them. Delivery stays opt-in. Pacto reaches GAR, GCR, ECR and ACR with cloud workload identity, and uses a pull Secret only once `registryPullSecret` names it.
