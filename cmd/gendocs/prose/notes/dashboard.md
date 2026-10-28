It auto-detects sources: pass OCI repositories as arguments, or run it next to the operator (with a kubeconfig) and it discovers OCI repositories from each Pacto resource's `status.contract.resolvedRef`. Use `--no-cache` for a cold start (it skips scanning pre-existing cached bundles; bundles fetched during the session are still cached).

For a tour of what the dashboard surfaces, see [For platform engineers](platform-engineers.md); to run it as a container, see [Dashboard container](dashboard-docker.md).
