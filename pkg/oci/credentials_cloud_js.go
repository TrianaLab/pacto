//go:build js

package oci

// The WASM demo links no cloud SDK, so it has no cloud sources.
func cloudSources() []namedKeychain { return nil }

// CloudKeychain is empty in the WASM build.
func CloudKeychain() *PactoKeychain { return &PactoKeychain{} }
