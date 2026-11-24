/*
Copyright 2026 TrianaLab.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	core "github.com/trianalab/pacto/v4/pkg/delivery"
)

// TestReadRecorderKey: the chart's pin signer gives one key, without its pin
// streams; no file names none; a file that does not read, parse or hold one
// key stops the operator.
func TestReadRecorderKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	pemKey, _ := core.PublicKeyPEM(pub)
	indent := "      " + strings.ReplaceAll(strings.TrimSpace(pemKey), "\n", "\n      ")
	one := "keys:\n  - name: recorder\n    publicKey: |\n" + indent + "\n    streams: [pins/*/*]\n"
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	k, err := readRecorderKey(write("one.yaml", one))
	if err != nil || k == nil || k.Name != "recorder" || k.Streams != nil || k.PublicKey == "" {
		t.Fatalf("key %+v, %v", k, err)
	}
	if k, err := readRecorderKey(""); k != nil || err != nil {
		t.Fatalf("no file: %+v, %v", k, err)
	}
	two := one + strings.Replace(one, "keys:\n", "", 1)
	for name, path := range map[string]string{"missing": filepath.Join(dir, "none"),
		"garbage": write("bad.yaml", "keys: [{"), "two keys": write("two.yaml", two)} {
		if _, err := readRecorderKey(path); err == nil || !strings.Contains(err.Error(), "--recorder-trust-file") {
			t.Errorf("%s: %v", name, err)
		}
	}
}
