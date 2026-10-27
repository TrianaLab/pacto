---
"@pacto/k8s-module": minor
---

The `pacto-operator` chart can install [zot](https://zotregistry.dev) and
[Dex](https://dexidp.io) from their official charts (`zot.enabled`,
`dex.enabled`, both off by default). The chart ships the profile
`values-quickstart.yaml`, which turns both on for an evaluation cluster: zot
holds the delivery record streams, Dex signs people in to the dashboard and
the chart generates the dashboard's client secret, the first sign-in password
and the recorder's signing key (`delivery.recorder.signing.ed25519.generate`)
at install. See
[Quickstart with a bundled registry and sign-in](https://pacto.run/latest/integrations/kubernetes/installation/#quickstart-with-a-bundled-registry-and-sign-in).
