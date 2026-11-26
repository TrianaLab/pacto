// Package managedres reads the managed cloud resources a Terraform or
// OpenTofu state describes, as names only: the resource type, its address,
// the identity the external catalog classifies it as and the observed
// engine. No other attribute value is ever held in a returned value. It
// imports no Kubernetes or cloud client, so the CLI, the dashboard and the
// WASM demo can link it.
package managedres
