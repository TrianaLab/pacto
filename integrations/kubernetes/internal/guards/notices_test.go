// Package guards holds checks on files the module ships but does not compile:
// the delivery image's license notices and the generated CRDs.
package guards

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// notices is copied into the delivery image. Regenerate it with
// UPDATE_NOTICES=1 go test ./internal/guards/.
const notices = "../../cmd/delivery/THIRD_PARTY_NOTICES"

// The delivery image builds with GO_TAGS=kms (see the Dockerfile), which links
// MPL-2.0 code (hashicorp/vault/api and its helpers) besides the usual MIT, BSD
// and Apache-2.0 modules. Every one of those licenses asks that a binary carry
// its text, and MPL-2.0 also that recipients learn where the source is, so the
// image ships every linked module's license text and source location.
func TestDeliveryImageCarriesEveryLinkedLicense(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "list", "-deps", "-tags", "kms",
		"-f", "{{with .Module}}{{.Path}}\t{{.Version}}\t{{.Dir}}{{end}}", "../../cmd/delivery")
	// The image is linux (amd64 and arm64 link the same modules); pin it so a
	// darwin host renders what the image links, not what the host links.
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	goroot, err := exec.CommandContext(ctx, "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	want, err := render(out, strings.TrimSpace(string(goroot)))
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_NOTICES") != "" {
		if err := os.WriteFile(notices, want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(notices)
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(dockerfile, []byte("COPY integrations/kubernetes/cmd/delivery/THIRD_PARTY_NOTICES /THIRD_PARTY_NOTICES")) {
		t.Error("the delivery image must copy THIRD_PARTY_NOTICES")
	}
	// Every Go binary links the runtime and standard library, which go list
	// reports with no module, so they need their own entry.
	if !bytes.Contains(got, []byte("\nGo standard library and runtime: BSD, text ")) {
		t.Error("THIRD_PARTY_NOTICES must carry the Go standard library's license")
	}
	// procfs is linked on linux only, so it proves the list is the image's.
	if !bytes.Contains(got, []byte("\ngithub.com/prometheus/procfs ")) {
		t.Error("THIRD_PARTY_NOTICES must list what the linux image links, including github.com/prometheus/procfs")
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("%s is stale for what cmd/delivery links with -tags kms: run UPDATE_NOTICES=1 go test ./internal/guards/", notices)
	}
}

func render(list []byte, goroot string) ([]byte, error) {
	type mod struct{ path, version, dir string }
	seen := map[string]mod{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(list)), "\n") {
		f := strings.Split(line, "\t")
		// No version is a workspace module: Pacto's own, under its MIT LICENSE.
		if len(f) != 3 || f[1] == "" {
			continue
		}
		seen[f[0]] = mod{f[0], f[1], f[2]}
	}
	paths := make([]string, 0, len(seen))
	for p := range seen {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var b, texts bytes.Buffer
	b.WriteString("Third-party software in the Pacto delivery image\n\n" +
		"The delivery binary links the Go standard library and the Go modules\n" +
		"below. Each entry gives the module, its version, its license, where its\n" +
		"source is and which of the license texts that follow it carries; modules\n" +
		"with identical texts share one.\n" +
		"MPL-2.0 modules are used unmodified, and their source is at the address given.\n\n")
	index := map[string]int{}
	// The Go LICENSE text does not change between releases, so the entry names
	// no version and stays the same whichever toolchain renders it.
	text, kind, err := licenseOf(goroot)
	if err != nil {
		return nil, fmt.Errorf("go standard library: %w", err)
	}
	index[text] = 1
	fmt.Fprintf(&texts, "\n================================================================\nText 1\n\n%s", text)
	fmt.Fprintf(&b, "Go standard library and runtime: %s, text 1\n  source https://go.dev/dl/\n", kind)
	for _, p := range paths {
		m := seen[p]
		text, kind, err := licenseOf(m.dir)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", m.path, m.version, err)
		}
		n, ok := index[text]
		if !ok {
			n = len(index) + 1
			index[text] = n
			fmt.Fprintf(&texts, "\n================================================================\nText %d\n\n%s", n, text)
		}
		fmt.Fprintf(&b, "%s %s: %s, text %d\n  source https://proxy.golang.org/%s/@v/%s.zip\n",
			m.path, m.version, kind, n, escape(m.path), m.version)
	}
	b.Write(texts.Bytes())
	return b.Bytes(), nil
}

// licenseOf returns the module's license and NOTICE texts and the kind of its
// license. A module with no license file fails: someone has to look at it.
func licenseOf(dir string) (string, string, error) {
	var texts []string
	kind := ""
	for _, name := range []string{"LICENSE", "LICENSE.txt", "LICENSE.md", "LICENCE", "COPYING", "NOTICE", "NOTICE.txt", "NOTICE.md"} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		// Trailing blanks carry no meaning and trip the repository's whitespace gate.
		lines := strings.Split(strings.ReplaceAll(strings.TrimSpace(string(raw)), "\r\n", "\n"), "\n")
		for i, l := range lines {
			lines[i] = strings.TrimRight(l, " \t")
		}
		s := strings.Join(lines, "\n")
		texts = append(texts, s+"\n")
		if kind == "" && !strings.HasPrefix(name, "NOTICE") {
			kind = classify(s)
		}
	}
	if kind == "" {
		return "", "", fmt.Errorf("no license file in %s", dir)
	}
	return strings.Join(texts, "\n"), kind, nil
}

func classify(s string) string {
	switch {
	case strings.Contains(s, "Mozilla Public License"):
		return "MPL-2.0"
	case strings.Contains(s, "Apache License"):
		return "Apache-2.0"
	case strings.Contains(s, "Permission is hereby granted, free of charge"):
		return "MIT"
	case strings.Contains(s, "Redistribution and use in source and binary forms"):
		return "BSD"
	case strings.Contains(s, "Permission to use, copy, modify, and/or distribute"):
		return "ISC"
	}
	return "see the license text"
}

// escape is the module proxy's case encoding: an upper-case letter becomes '!'
// and its lower-case form.
func escape(path string) string {
	var b strings.Builder
	for _, r := range path {
		if r >= 'A' && r <= 'Z' {
			b.WriteByte('!')
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
