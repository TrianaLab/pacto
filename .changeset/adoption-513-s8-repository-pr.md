---
"@pacto/core": minor
"@pacto/k8s-module": patch
---

pacto declare names the service's source repository and folder, and how it
found them, in the new declaration.repository block of pacto.inferred.yaml:
from a verified keyless signature or signed SLSA provenance attestation, the
GitOps source, the image's org.opencontainers.image.source label or a
--repo-map file, with --repository and --folder overriding every source.
pacto declare --pr --yes then commits the bundle's pacto.yaml and
pacto.inferred.yaml on a new branch and opens a pull request on GitHub,
GitLab, Bitbucket Cloud or Gitea/Forgejo with your own Git token, from the
host's environment variable or git credential fill. An unsigned label, an
agent's repository and a public repository need a person's --repository.
pacto_declare gains pr, repository, folder and repo_map; a stdio server
opens the pull request and an HTTP server refuses it with
PullRequestNeedsLocalToken. The dashboard Declare panel shows the --pr
command and never holds a Git credential. pkg/publish gains
ProvenanceSigners.
