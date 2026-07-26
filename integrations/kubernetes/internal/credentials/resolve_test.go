/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

package credentials

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestFromSecret_OpaqueToken(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Data:       map[string][]byte{"token": []byte("ghp_abc")},
	}
	auth, err := FromSecret(secret, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.RegistryToken != "ghp_abc" {
		t.Fatalf("expected token ghp_abc, got %s", auth.RegistryToken)
	}
}

func TestFromSecret_OpaqueUsernamePassword(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Data:       map[string][]byte{"username": []byte("user"), "password": []byte("pass")},
	}
	auth, err := FromSecret(secret, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "user" || auth.Password != "pass" {
		t.Fatalf("expected user/pass, got %s/%s", auth.Username, auth.Password)
	}
}

func TestFromSecret_OpaqueTokenPrecedence(t *testing.T) {
	// When both token and username/password are set, token wins
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Data: map[string][]byte{
			"token":    []byte("tok"),
			"username": []byte("user"),
			"password": []byte("pass"),
		},
	}
	auth, err := FromSecret(secret, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.RegistryToken != "tok" {
		t.Fatal("expected token precedence over username/password")
	}
}

func TestFromSecret_OpaqueInvalidKeys(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Data:       map[string][]byte{"something": []byte("else")},
	}
	_, err := FromSecret(secret, "")
	if err == nil {
		t.Fatal("expected error for invalid keys")
	}
	if !strings.Contains(err.Error(), "must contain") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_ExactMatch(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Username: "user", Password: "pass"},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	auth, err := FromSecret(secret, "ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "user" || auth.Password != "pass" {
		t.Fatalf("expected user/pass, got %s/%s", auth.Username, auth.Password)
	}
}

func TestFromSecret_DockerConfigJSON_SchemeStripped(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"https://ghcr.io/v2/": {Username: "u", Password: "p"},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	auth, err := FromSecret(secret, "ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "u" {
		t.Fatalf("expected username u, got %s", auth.Username)
	}
}

func TestFromSecret_DockerConfigJSON_AuthField(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte("myuser:mypass"))
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Auth: encoded},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	auth, err := FromSecret(secret, "ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "myuser" || auth.Password != "mypass" {
		t.Fatalf("expected myuser/mypass, got %s/%s", auth.Username, auth.Password)
	}
}

func TestFromSecret_DockerConfigJSON_NoMatchingRegistry(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"docker.io": {Username: "u", Password: "p"},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for no matching registry")
	}
	if !strings.Contains(err.Error(), "no auth entry") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_MissingKey(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for missing key")
	}
	if !strings.Contains(err.Error(), "missing key") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_MalformedJSON(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{bad")},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_EmptyAuths(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for empty auths")
	}
	if !strings.Contains(err.Error(), "no auth entries") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_InvalidBase64Auth(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Auth: "not-valid-base64!!!"},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "invalid base64") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_AuthFieldBadFormat(t *testing.T) {
	// base64 of just "nocolon"
	encoded := base64.StdEncoding.EncodeToString([]byte("nocolon"))
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Auth: encoded},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for bad auth format")
	}
	if !strings.Contains(err.Error(), "base64(username:password)") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_EmptyEntry(t *testing.T) {
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for empty entry")
	}
	if !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFromSecret_DockerConfigJSON_AuthFieldEmptyUsername(t *testing.T) {
	// base64 of ":password" (empty username)
	encoded := base64.StdEncoding.EncodeToString([]byte(":password"))
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Auth: encoded},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}
	_, err := FromSecret(secret, "ghcr.io")
	if err == nil {
		t.Fatal("expected error for empty username in auth field")
	}
}

func TestNormalizeRegistryHost(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"ghcr.io", "ghcr.io"},
		{"https://ghcr.io", "ghcr.io"},
		{"https://ghcr.io/v2/", "ghcr.io"},
		{"http://registry:5000/v2/", "registry:5000"},
		{"docker.io", "index.docker.io"},
		{"https://docker.io/v1/", "index.docker.io"},
		{"index.docker.io", "index.docker.io"},
	}
	for _, tt := range tests {
		got := normalizeRegistryHost(tt.input)
		if got != tt.want {
			t.Errorf("normalizeRegistryHost(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestRegistryFromRef(t *testing.T) {
	tests := []struct {
		ref, want string
	}{
		{"ghcr.io/org/repo:tag", "ghcr.io"},
		{"ghcr.io/org/repo", "ghcr.io"},
		{"registry:5000/org/repo:1.0", "registry:5000"},
		{"oci://ghcr.io/org/repo", "ghcr.io"},
		{"ghcr.io/org/repo@sha256:abc", "ghcr.io"},
		{"ubuntu", "index.docker.io"},
		{"library/ubuntu", "index.docker.io"},
		{"my.registry.com/path/image", "my.registry.com"},
	}
	for _, tt := range tests {
		got := RegistryFromRef(tt.ref)
		if got != tt.want {
			t.Errorf("RegistryFromRef(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestMergeToDockerConfigJSON_OpaqueSecrets(t *testing.T) {
	s1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s1"},
		Data:       map[string][]byte{"username": []byte("u1"), "password": []byte("p1"), "registry": []byte("ghcr.io")},
	}
	result, err := MergeToDockerConfigJSON([]*corev1.Secret{s1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	if err := json.Unmarshal(result, &cfg); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	entry, ok := cfg.Auths["ghcr.io"]
	if !ok {
		t.Fatal("expected ghcr.io entry for opaque secret with registry key")
	}
	if entry.Username != "u1" || entry.Password != "p1" {
		t.Fatalf("expected u1/p1, got %s/%s", entry.Username, entry.Password)
	}
}

func TestMergeToDockerConfigJSON_OpaqueWithoutRegistryKey(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "no-registry"},
		Data:       map[string][]byte{"username": []byte("u"), "password": []byte("p")},
	}
	_, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err == nil {
		t.Fatal("expected error for opaque secret without registry key")
	}
	if !strings.Contains(err.Error(), "registry") {
		t.Fatalf("expected error mentioning 'registry', got: %v", err)
	}
}

func TestMergeToDockerConfigJSON_OpaqueRegistryNormalized(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Data:       map[string][]byte{"username": []byte("u"), "password": []byte("p"), "registry": []byte("https://ghcr.io/v2/")},
	}
	result, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	_ = json.Unmarshal(result, &cfg)
	if _, ok := cfg.Auths["ghcr.io"]; !ok {
		t.Fatal("expected normalized ghcr.io entry")
	}
}

func TestMergeToDockerConfigJSON_DockerConfigSecrets(t *testing.T) {
	inner := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io":   {Username: "u1", Password: "p1"},
		"docker.io": {Username: "u2", Password: "p2"},
	}}
	raw, _ := json.Marshal(inner)

	s1 := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s1"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	result, err := MergeToDockerConfigJSON([]*corev1.Secret{s1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	_ = json.Unmarshal(result, &cfg)

	if _, ok := cfg.Auths["ghcr.io"]; !ok {
		t.Error("expected ghcr.io entry")
	}
	// docker.io should be normalized to index.docker.io
	if _, ok := cfg.Auths["index.docker.io"]; !ok {
		t.Error("expected index.docker.io entry (normalized from docker.io)")
	}
}

func TestMergeToDockerConfigJSON_Mixed(t *testing.T) {
	inner := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Username: "gcr-user", Password: "gcr-pass"},
	}}
	raw, _ := json.Marshal(inner)

	secrets := []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "opaque"},
			Data:       map[string][]byte{"token": []byte("tok123"), "registry": []byte("registry.example.com")},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "docker"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
		},
	}

	result, err := MergeToDockerConfigJSON(secrets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	_ = json.Unmarshal(result, &cfg)

	if len(cfg.Auths) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(cfg.Auths))
	}
	if _, ok := cfg.Auths["registry.example.com"]; !ok {
		t.Error("expected registry.example.com entry")
	}
	if _, ok := cfg.Auths["ghcr.io"]; !ok {
		t.Error("expected ghcr.io entry")
	}
}

func TestMergeToDockerConfigJSON_LaterOverridesEarlier(t *testing.T) {
	inner1 := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Username: "first", Password: "first"},
	}}
	raw1, _ := json.Marshal(inner1)

	inner2 := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Username: "second", Password: "second"},
	}}
	raw2, _ := json.Marshal(inner2)

	secrets := []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "s1"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw1},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "s2"},
			Type:       corev1.SecretTypeDockerConfigJson,
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw2},
		},
	}

	result, err := MergeToDockerConfigJSON(secrets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	_ = json.Unmarshal(result, &cfg)

	if cfg.Auths["ghcr.io"].Username != "second" {
		t.Fatalf("expected later secret to override, got %s", cfg.Auths["ghcr.io"].Username)
	}
}

func TestMergeToDockerConfigJSON_InvalidOpaque(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Data:       map[string][]byte{"registry": []byte("ghcr.io"), "nope": []byte("x")},
	}
	_, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err == nil {
		t.Fatal("expected error for invalid opaque secret")
	}
}

func TestMergeToDockerConfigJSON_InvalidDockerConfig(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte("{bad")},
	}
	_, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err == nil {
		t.Fatal("expected error for malformed dockerconfigjson")
	}
}

func TestMergeToDockerConfigJSON_MissingDockerConfigKey(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "bad"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{},
	}
	_, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err == nil {
		t.Fatal("expected error for missing key")
	}
}

func TestMergeToDockerConfigJSON_OpaqueToken(t *testing.T) {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "tok"},
		Data:       map[string][]byte{"token": []byte("mytoken"), "registry": []byte("ghcr.io")},
	}
	result, err := MergeToDockerConfigJSON([]*corev1.Secret{s})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var cfg dockerConfigJSON
	_ = json.Unmarshal(result, &cfg)

	entry := cfg.Auths["ghcr.io"]
	// Token-based opaque secrets encode as _token:TOKEN
	decoded, _ := base64.StdEncoding.DecodeString(entry.Auth)
	if string(decoded) != "_token:mytoken" {
		t.Fatalf("expected _token:mytoken, got %s", decoded)
	}
}

func TestFromSecret_DockerConfigJSON_DockerHubNormalization(t *testing.T) {
	// Secret keyed as "docker.io", queried as "index.docker.io"
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"docker.io": {Username: "u", Password: "p"},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	// Both "docker.io" and "index.docker.io" should match
	for _, registry := range []string{"docker.io", "index.docker.io"} {
		auth, err := FromSecret(secret, registry)
		if err != nil {
			t.Fatalf("registry=%q: unexpected error: %v", registry, err)
		}
		if auth.Username != "u" {
			t.Fatalf("registry=%q: expected u, got %s", registry, auth.Username)
		}
	}
}

func TestFromSecret_DockerConfigJSON_UsernamePasswordPrecedence(t *testing.T) {
	// When both username/password and auth are set, username/password wins
	encoded := base64.StdEncoding.EncodeToString([]byte("auth-user:auth-pass"))
	cfg := dockerConfigJSON{Auths: map[string]dockerConfigEntry{
		"ghcr.io": {Username: "explicit", Password: "explicit", Auth: encoded},
	}}
	raw, _ := json.Marshal(cfg)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "s"},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: raw},
	}

	auth, err := FromSecret(secret, "ghcr.io")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if auth.Username != "explicit" {
		t.Fatalf("expected explicit username to take precedence, got %s", auth.Username)
	}
}
