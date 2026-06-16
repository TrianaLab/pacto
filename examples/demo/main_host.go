//go:build !(js && wasm)

package main

import "fmt"

// This binary is meant to be built for the browser (GOOS=js GOARCH=wasm). The
// host build exists only so `go build ./...` and `go test ./...` work during
// development and CI; running it does nothing useful.
func main() {
	fmt.Println("pacto dashboard demo: build with GOOS=js GOARCH=wasm (see Makefile)")
}
