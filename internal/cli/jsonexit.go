package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// Execute runs root with args. Under --output-format json, a failure cobra
// reports before any RunE (a flag it cannot parse, a wrong argument count,
// the root's pre-run) still writes the one exit document when nothing reached
// stdout; wrapJSONExit covers the failures inside a RunE.
func Execute(ctx context.Context, root *cobra.Command, args []string) error {
	out := &countingWriter{w: root.OutOrStdout()}
	root.SetOut(out)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err != nil && out.n == 0 && wantsJSON(args) {
		_ = json.NewEncoder(out.w).Encode(jsonExit{jsonExitOf(err)})
	}
	return err
}

// wantsJSON reports whether args, or PACTO_OUTPUT_FORMAT, ask for json. It
// reads the raw arguments: a parse failure may stop before the flag.
// ponytail: a config file's output-format is not read here; cobra failed
// before the pre-run that reads it.
func wantsJSON(args []string) bool {
	fs := pflag.NewFlagSet("", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	format := fs.String(outputFormatKey, os.Getenv("PACTO_OUTPUT_FORMAT"), "")
	_ = fs.Parse(args)
	return *format == "json"
}

// countingWriter is w that remembers whether anything was written to it.
type countingWriter struct {
	w io.Writer
	n int
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += n
	return n, err
}

// jsonExit is the document a command that fails under --output-format json
// answers when it wrote nothing, so an agent always reads one JSON document.
type jsonExit struct {
	Exit exitDoc `json:"exit"`
}

// exitDoc is a failed command's exit code and the reason it gave.
type exitDoc struct {
	Code   int    `json:"code"`
	Reason string `json:"reason"`
}

// wrapJSONExit wraps every RunE of the tree under root: under
// --output-format json, a command that fails having written nothing to stdout
// writes {"exit":{"code":…,"reason":…}} there and returns its error unchanged.
// A command that wrote its result carries its exit inside it. cobra runs no
// hook after a failed RunE, so the wrapper is the one place that sees every
// failure.
func wrapJSONExit(root *cobra.Command, v *viper.Viper) {
	for _, c := range root.Commands() {
		wrapJSONExit(c, v)
	}
	run := root.RunE
	if run == nil {
		return
	}
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if v.GetString(outputFormatKey) != "json" {
			return run(cmd, args)
		}
		out := &countingWriter{w: cmd.OutOrStdout()}
		cmd.SetOut(out)
		// No production command sets its own writer, so restoring none hands
		// stdout back to the parent's, as before the call.
		defer cmd.SetOut(nil)
		err := run(cmd, args)
		if err != nil && out.n == 0 {
			_ = json.NewEncoder(out.w).Encode(jsonExit{jsonExitOf(err)})
		}
		return err
	}
}

// jsonExitOf is the exit code and reason of a failed command: an ExitError's code
// and reason, else 1 and the error, as main exits.
func jsonExitOf(err error) exitDoc {
	var exit *ExitError
	if errors.As(err, &exit) {
		return exitDoc{exit.Code, exit.Reason}
	}
	return exitDoc{1, err.Error()}
}

// printJSONExit prints v, a struct, as printJSON does and returns err; when
// err is non-nil the object also carries "exit", after v's own keys, so a command
// that answers its result and fails still writes one document with its exit.
func printJSONExit(cmd *cobra.Command, v any, err error) error {
	if err == nil {
		return printJSON(cmd, v)
	}
	raw, merr := json.Marshal(v)
	if merr != nil {
		return merr
	}
	exit, _ := json.Marshal(jsonExitOf(err)) // ponytail: two plain fields never fail to marshal
	sep := ","
	if bytes.Equal(raw, []byte("{}")) {
		sep = ""
	}
	raw = append(append(append(raw[:len(raw)-1:len(raw)-1], sep+`"exit":`...), exit...), '}')
	var out bytes.Buffer
	_ = json.Indent(&out, raw, "", "  ") // ponytail: raw is valid JSON by construction
	out.WriteByte('\n')
	if _, werr := cmd.OutOrStdout().Write(out.Bytes()); werr != nil {
		return werr
	}
	return err
}
