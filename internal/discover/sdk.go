package discover

import (
	"bufio"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/trianalab/pacto/v4/pkg/contract"
	"github.com/trianalab/pacto/v4/pkg/externalcatalog"
)

// Why a dependency manifest is not read, each the end of a fixed message.
const (
	unreadableNotParsed = "could not be parsed, so it is not read"
	unsupportedJSLock   = "the client major comes from package.json"
	unsupportedGradle   = "commit gradle.lockfile to have Pacto read it"
)

// sdkDep is one dependency a manifest lists: its name as written, its major
// (nil when unknown, never guessed) and the line that lists it.
type sdkDep struct {
	name  string
	major *int
	line  int
}

// sdkFormat reads the dependencies of one manifest; open reads a file beside
// it, nil when the folder has none. ok is false when it cannot be parsed.
type sdkFormat struct {
	ecosystem string
	read      func(data []byte, open func(name string) []byte) (deps []sdkDep, ok bool)
}

// sdkFormats are the manifests read, by base name; requirements*.txt is
// matched apart. Each is read only for dependency names and versions:
// registry URLs, resolved fields, replace paths, repositories and scripts
// never are.
var sdkFormats = map[string]sdkFormat{
	"go.mod":          {"go", goModDeps},
	"package.json":    {"npm", npmDeps},
	"poetry.lock":     {"pypi", poetryDeps},
	"pom.xml":         {"maven", mavenDeps},
	"gradle.lockfile": {"maven", gradleDeps},
}

// sdkNotRead are the manifests recognized but not read, by base name, each
// with why. A Gradle build file is one only without a gradle.lockfile beside
// it; a package-lock.json is read with its package.json.
var sdkNotRead = map[string]string{
	"yarn.lock": unsupportedJSLock, "pnpm-lock.yaml": unsupportedJSLock,
	"build.gradle": unsupportedGradle, "build.gradle.kts": unsupportedGradle,
	"package-lock.json": "",
}

// sdkFile reports whether the base name names a manifest sdkUses reads or
// names.
func sdkFile(base string) bool {
	_, read := sdkFormats[base]
	_, named := sdkNotRead[base]
	return read || named || requirementsFile(base)
}

func requirementsFile(base string) bool {
	ok, _ := path.Match("requirements*.txt", base)
	return ok
}

// readSDKFile reads a manifest; tests make it fail.
var readSDKFile = os.ReadFile

// sdkScan collects what sdkUses finds.
type sdkScan struct {
	dir      string
	files    map[string]bool // the manifests the walk visited
	xs       []External
	findings []contract.InferredFinding
	read     []string
}

// sdkUses are the external systems the client libraries in the folder dir's
// dependency manifests belong to, classified with cat, each only possible:
// a library in a manifest proves no call. It also returns what it could not
// read, as findings, and every file it read.
func (sc *Scan) sdkUses(dir string, cat *externalcatalog.Catalog) ([]External, []contract.InferredFinding, []string) {
	u := &sdkScan{dir: dir, files: map[string]bool{}}
	for _, f := range sc.sdkFiles {
		u.files[f] = true
	}
	for _, rel := range sc.sdkFiles {
		u.manifest(rel, cat)
	}
	return u.xs, u.findings, u.read
}

// manifest reads the manifest rel and matches every dependency it lists.
func (u *sdkScan) manifest(rel string, cat *externalcatalog.Catalog) {
	base, sibling := path.Base(rel), func(name string) string { return path.Join(path.Dir(rel), name) }
	if why, ok := sdkNotRead[base]; ok {
		if why != "" && (why != unsupportedGradle || !u.files[sibling("gradle.lockfile")]) {
			u.findings = append(u.findings, contract.InferredFinding{Code: CodeUnsupportedFile, Source: rel, Message: unsupportedMessage(rel, why)})
		}
		return
	}
	format, ok := sdkFormats[base]
	if !ok {
		format = sdkFormat{"pypi", requirementsDeps}
	}
	data, ok := u.readFile(rel)
	if !ok {
		return
	}
	deps, ok := format.read(data, func(name string) []byte {
		if p := sibling(name); u.files[p] {
			b, _ := u.readFile(p)
			return b
		}
		return nil
	})
	if !ok {
		u.findings = append(u.findings, contract.InferredFinding{Code: CodeUnreadable, Source: rel, Message: unreadableMessage(rel, unreadableNotParsed)})
	}
	for _, d := range deps {
		if m, ok := cat.MatchSDK(format.ecosystem, d.name); ok {
			u.xs = append(u.xs, External{Identity: string(m.Identity), Class: m.Class, Confidence: m.Confidence, Entry: m.Entry, Origin: m.Origin,
				Kind: "sdk", Path: rel, Line: d.line, Where: commentSafe(at(rel, d.line)), SDK: d.name, Major: d.major})
		}
	}
}

// readFile reads the regular file rel; a symlink or any other file is a
// finding and is never followed.
func (u *sdkScan) readFile(rel string) ([]byte, bool) {
	p := filepath.Join(u.dir, filepath.FromSlash(rel))
	why := unreadableFailed
	fi, err := os.Lstat(p)
	var data []byte
	switch {
	case err == nil && !fi.Mode().IsRegular():
		why = unreadableNotRegular
	case err == nil:
		if data, err = readSDKFile(p); err == nil {
			u.read = append(u.read, rel)
			return data, true
		}
	}
	u.findings = append(u.findings, contract.InferredFinding{Code: CodeUnreadable, Source: rel, Message: unreadableMessage(rel, why)})
	return nil, false
}

var majorRE = regexp.MustCompile(`^v?([0-9]+)(?:[.+-]|$)`)

// majorOf is the major of a version ("v76.3.0", "11.4.0"), nil when it has
// none.
func majorOf(version string) *int {
	m := majorRE.FindStringSubmatch(version)
	if m == nil {
		return nil
	}
	n, err := strconv.Atoi(m[1])
	if err != nil { // too large
		return nil
	}
	return &n
}

// goModDeps are the requires of a go.mod; the major is the version's, which
// a /vN path suffix always matches.
func goModDeps(data []byte, _ func(string) []byte) ([]sdkDep, bool) {
	f, err := modfile.ParseLax("go.mod", data, nil)
	if err != nil {
		return nil, false
	}
	var out []sdkDep
	for _, r := range f.Require {
		out = append(out, sdkDep{r.Mod.Path, majorOf(r.Mod.Version), r.Syntax.Start.Line})
	}
	return out, true
}

// exactRangeRE is an npm range that names one major: exact, ^X or ~X.
var exactRangeRE = regexp.MustCompile(`^[\^~]?v?[0-9]+(\.[0-9]+){0,2}(-[0-9A-Za-z.-]+)?$`)

// npmDeps are a package.json's dependencies and optionalDependencies (a
// devDependency is a tool, not a runtime client), the major from
// package-lock.json, else from an exact range.
func npmDeps(data []byte, open func(string) []byte) ([]sdkDep, bool) {
	if !json.Valid(data) {
		return nil, false
	}
	lock := npmLockMajors(open("package-lock.json"))
	var out []sdkDep
	jsonKeys(data, 0, func(key string, _, start int, _ json.RawMessage) {
		if key != "dependencies" && key != "optionalDependencies" {
			return
		}
		jsonKeys(data, start, func(name string, line, _ int, raw json.RawMessage) {
			major, ok := lock[name]
			if !ok {
				var rng string
				if json.Unmarshal(raw, &rng) == nil && exactRangeRE.MatchString(rng) {
					major = majorOf(strings.TrimLeft(rng, "^~"))
				}
			}
			out = append(out, sdkDep{name, major, line})
		})
	})
	return out, true
}

// npmLockMajors are the majors a package-lock.json locks, by package name:
// lockfile v2 and v3 packages, v1 dependencies. Only versions are read.
func npmLockMajors(data []byte) map[string]*int {
	var lock struct {
		Packages     map[string]struct{ Version string } `json:"packages"`
		Dependencies map[string]struct{ Version string } `json:"dependencies"`
	}
	if json.Unmarshal(data, &lock) != nil {
		return nil // ponytail: an unparsable lockfile leaves the package.json range
	}
	out := map[string]*int{}
	for name, d := range lock.Dependencies {
		out[name] = majorOf(d.Version)
	}
	for key, p := range lock.Packages {
		if name, ok := strings.CutPrefix(key, "node_modules/"); ok {
			out[name] = majorOf(p.Version)
		}
	}
	return out
}

// jsonKeys calls visit with each key of the JSON object that starts at the
// offset start of data, the line the key is on, and its value with the
// offset it starts at. data is valid JSON; a value that is not an object
// has no keys.
func jsonKeys(data []byte, start int, visit func(key string, line, start int, raw json.RawMessage)) {
	dec := json.NewDecoder(bytes.NewReader(data[start:]))
	if t, _ := dec.Token(); t != json.Delim('{') {
		return
	}
	for dec.More() {
		t, _ := dec.Token() // a key: data is valid JSON
		line := 1 + bytes.Count(data[:start+int(dec.InputOffset())], []byte("\n"))
		var raw json.RawMessage
		_ = dec.Decode(&raw)
		visit(t.(string), line, start+int(dec.InputOffset())-len(raw), raw)
	}
}

// requirementRE is a requirements.txt line: a name, extras and, when pinned
// with == or ~=, a version.
var requirementRE = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:\[[^\]]*\])?\s*(==|~=)?\s*([^,;\s]*)`)

// requirementsDeps are the names a requirements file lists, with a major only
// when pinned with == or ~=. Options, -r, -e and URLs are skipped.
func requirementsDeps(data []byte, _ func(string) []byte) ([]sdkDep, bool) {
	var out []sdkDep
	eachLine(data, func(n int, line string) {
		if i := strings.Index(line, " #"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "-") || strings.Contains(line, "://") || strings.Contains(line, "@") {
			return
		}
		if m := requirementRE.FindStringSubmatch(line); m != nil {
			d := sdkDep{name: m[1], line: n}
			if m[2] != "" {
				d.major = majorOf(m[3])
			}
			out = append(out, d)
		}
	})
	return out, true
}

// poetryDeps are the name and version of each [[package]] of a poetry.lock,
// read line by line.
func poetryDeps(data []byte, _ func(string) []byte) ([]sdkDep, bool) {
	var out []sdkDep
	inPackage := false
	eachLine(data, func(n int, line string) {
		line = strings.TrimSpace(line)
		switch {
		case line == "[[package]]":
			inPackage = true
			out = append(out, sdkDep{})
		case strings.HasPrefix(line, "["):
			inPackage = false
		case inPackage:
			key, value, _ := strings.Cut(line, "=")
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.TrimSpace(key) {
			case "name":
				out[len(out)-1].name, out[len(out)-1].line = value, n
			case "version":
				out[len(out)-1].major = majorOf(value)
			}
		}
	})
	return out, true
}

// gradleDeps are the group:artifact:version=configurations lines of a
// gradle.lockfile.
func gradleDeps(data []byte, _ func(string) []byte) ([]sdkDep, bool) {
	var out []sdkDep
	eachLine(data, func(n int, line string) {
		coord, _, _ := strings.Cut(strings.TrimSpace(line), "=")
		if parts := strings.Split(coord, ":"); len(parts) == 3 && !strings.HasPrefix(coord, "#") {
			out = append(out, sdkDep{parts[0] + ":" + parts[1], majorOf(parts[2]), n})
		}
	})
	return out, true
}

// eachLine calls visit with each line of data and its number.
func eachLine(data []byte, visit func(n int, line string)) {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(nil, len(data)+1)
	for n := 1; s.Scan(); n++ {
		visit(n, s.Text())
	}
}

// pomDep is a pom.xml dependency as it is read.
type pomDep struct {
	group, artifact, version string
	line                     int
}

// mavenDeps are the project's dependencies in a pom.xml, a ${property}
// version resolved only against the file's own properties.
// dependencyManagement and repositories are never read.
func mavenDeps(data []byte, _ func(string) []byte) ([]sdkDep, bool) {
	p := &pomReader{dec: xml.NewDecoder(bytes.NewReader(data)), props: map[string]string{}}
	for {
		tok, err := p.dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, false
		}
		p.token(tok)
	}
	var out []sdkDep
	for _, d := range p.deps {
		v := d.version
		if name, ok := strings.CutPrefix(v, "${"); ok {
			v = p.props[strings.TrimSuffix(name, "}")]
		}
		out = append(out, sdkDep{d.group + ":" + d.artifact, majorOf(v), d.line})
	}
	return out, true
}

// pomReader walks a pom.xml's tokens.
type pomReader struct {
	dec   *xml.Decoder
	stack []string
	text  strings.Builder
	props map[string]string
	deps  []pomDep
}

func (p *pomReader) token(tok xml.Token) {
	switch t := tok.(type) {
	case xml.StartElement:
		p.stack = append(p.stack, t.Name.Local)
		p.text.Reset()
		if strings.Join(p.stack, "/") == "project/dependencies/dependency" {
			line, _ := p.dec.InputPos()
			p.deps = append(p.deps, pomDep{line: line})
		}
	case xml.CharData:
		p.text.Write(t)
	case xml.EndElement:
		p.end(strings.TrimSpace(p.text.String()))
		p.stack = p.stack[:len(p.stack)-1]
		p.text.Reset()
	}
}

// end keeps the text v of the element that ends, when it is a property or
// a field of a project dependency.
func (p *pomReader) end(v string) {
	switch strings.Join(p.stack[:len(p.stack)-1], "/") {
	case "project/properties":
		p.props[p.stack[len(p.stack)-1]] = v
	case "project/dependencies/dependency":
		d := &p.deps[len(p.deps)-1]
		switch p.stack[len(p.stack)-1] {
		case "groupId":
			d.group = v
		case "artifactId":
			d.artifact = v
		case "version":
			d.version = v
		}
	}
}
