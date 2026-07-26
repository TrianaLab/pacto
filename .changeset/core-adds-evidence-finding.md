---
"@pacto/core": minor
"@pacto/cli": minor
"@pacto/dashboard-image": minor
---

Core adds pkg/evidence + pkg/finding + the pure Evaluate API (Pacto 2.0 compliance
model), consumed by the Kubernetes integration. The core line must publish a
version containing them before the integration can pin it.
