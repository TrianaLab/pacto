---
"@pacto/core": minor
"@pacto/k8s-module": minor
---

Profile idp turns on the Evidence Server and the recorder only where their
prerequisites hold, detects Crossplane, can bootstrap upstream Crossplane on
request and suggests one provisioning step when nothing reported.
`pacto fleet providers`, `pacto_fleet_status.providers` and Detected providers
on Data sources say why each piece is on or off, and
`installation.provisioning.engine` reads `crossplane`, `adapter`, `none` or
`unknown`. An explicit `delivery.recorder.enabled: false` keeps the recorder
off under idp.
