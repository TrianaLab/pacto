package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/trianalab/pacto/v4/internal/testutil"
)

// jsonExitRoot is a root with one leaf that writes out and returns err, wrapped
// by wrapJSONExit under format.
func jsonExitRoot(format, out string, err error) (*cobra.Command, *bytes.Buffer) {
	v := viper.New()
	v.Set(outputFormatKey, format)
	root := &cobra.Command{Use: "pacto", SilenceErrors: true, SilenceUsage: true}
	root.AddCommand(&cobra.Command{Use: "leaf", RunE: func(cmd *cobra.Command, _ []string) error {
		_, _ = cmd.OutOrStdout().Write([]byte(out))
		return err
	}})
	wrapJSONExit(root, v)
	buf := &bytes.Buffer{}
	root.SetOut(buf)
	root.SetArgs([]string{"leaf"})
	return root, buf
}

func TestJSONExitEnvelopeWhenNothingWritten(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&ExitError{Code: 2, Reason: "unverified: no consumer source"}, `{"exit":{"code":2,"reason":"unverified: no consumer source"}}` + "\n"},
		{errors.New("no registry"), `{"exit":{"code":1,"reason":"no registry"}}` + "\n"},
	} {
		root, buf := jsonExitRoot("json", "", c.err)
		_ = root.Execute()
		expect(t, buf.String() == c.want, "stdout = %q, want %q", buf.String(), c.want)
	}
}

func TestJSONExitLeavesWrittenDocumentAlone(t *testing.T) {
	root, buf := jsonExitRoot("json", `{"exit":1}`, &ExitError{Code: 1})
	_ = root.Execute()
	expect(t, buf.String() == `{"exit":1}`, "stdout = %q", buf.String())
}

func TestJSONExitTextModeUnchanged(t *testing.T) {
	for _, format := range []string{"text", "markdown"} {
		root, buf := jsonExitRoot(format, "", errors.New("boom"))
		_ = root.Execute()
		expect(t, buf.Len() == 0, "%s: stdout = %q", format, buf.String())
	}
}

func TestJSONExitKeepsError(t *testing.T) {
	want := &ExitError{Code: 2}
	root, _ := jsonExitRoot("json", "", want)
	err := root.Execute()
	expect(t, errors.Is(err, want), "err = %v", err)
	root, _ = jsonExitRoot("json", "", nil)
	expect(t, root.Execute() == nil, "a successful command returns nil")
}

func TestPrintJSONExit(t *testing.T) {
	type doc struct {
		Valid bool `json:"valid"`
	}
	failed := &ExitError{Code: 2, Reason: "unverified"}
	for _, c := range []struct {
		v    any
		err  error
		want string
	}{
		{doc{true}, nil, "{\n  \"valid\": true\n}\n"},
		{doc{false}, failed, "{\n  \"valid\": false,\n  \"exit\": {\n    \"code\": 2,\n    \"reason\": \"unverified\"\n  }\n}\n"},
		{struct{}{}, errors.New("boom"), "{\n  \"exit\": {\n    \"code\": 1,\n    \"reason\": \"boom\"\n  }\n}\n"},
	} {
		cmd, buf := testCmd()
		err := printJSONExit(cmd, c.v, c.err)
		expect(t, errors.Is(err, c.err) && buf.String() == c.want, "err %v, stdout %q, want %q", err, buf.String(), c.want)
	}
	cmd, _ := testCmd()
	expect(t, printJSONExit(cmd, struct{ C chan int }{}, failed) != nil, "an unmarshalable value is an error")
	cmd.SetOut(errWriter{})
	expect(t, printJSONExit(cmd, doc{}, failed).Error() == "write error", "a write error is returned")
}

// TestImpactJSONCarriesExit: pacto impact answers its result and, when it
// exits non-zero, the exit inside that one document.
func TestImpactJSONCarriesExit(t *testing.T) {
	statsOff(t)
	oldDir, newDir := impactDirs(t, true)
	root := t.TempDir()
	testutil.WriteConsumer(t, filepath.Join(root, "webapp"), "^1.0.0")
	stdout, stderr, err := runRoot(t, defaultDeps(), nil, "impact", oldDir, newDir, "--local", root, "--output-format", "json")
	var res struct {
		Gate string
		Exit *exitDoc `json:"exit"`
	}
	expect(t, json.Unmarshal([]byte(stdout), &res) == nil, "stdout is no JSON document: %q", stdout)
	expect(t, err != nil && res.Exit != nil && res.Exit.Reason != "", "err %v, exit %+v (stderr %q)", err, res.Exit, stderr)
}
