package discover

import (
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// dockers reads every Dockerfile, the root one first (dockerRank): EXPOSE
// gives ports, HEALTHCHECK a health capability and the OCI description label
// the summary. A deeper Dockerfile that gives the same field is outranked.
func (r *reader) dockers() {
	slices.SortFunc(r.dockerfiles, func(a, b string) int { return strings.Compare(dockerRank(a), dockerRank(b)) })
	for i, p := range r.dockerfiles {
		for _, ins := range contract.DockerInstructions(r.read(p)) {
			r.dockerInstruction(p, rankDockerfile+i, ins)
		}
	}
}

// dockerInstruction reads one instruction of the Dockerfile p.
func (r *reader) dockerInstruction(p string, rank int, ins contract.DockerInstruction) {
	fields := strings.Fields(ins.Text)
	if len(fields) == 0 {
		return
	}
	src := fmt.Sprintf("%s:%d", p, ins.Line)
	add := func(field, value, declare string) {
		r.add(entry{Inferred: contract.Inferred{Field: field, Value: value, Source: src, Confidence: contract.ConfidenceLikely, Declare: declare}, rank: rank, file: p})
	}
	switch strings.ToUpper(fields[0]) {
	case "EXPOSE":
		for _, tok := range fields[1:] {
			n, proto, ok := exposedPort(tok)
			if !ok {
				r.find(CodeUnresolved, src, unresolvedPortMessage(src))
				continue
			}
			add(fmt.Sprintf("ports[%d]", n), fmt.Sprintf("%d/%s", n, proto), "")
		}
	case "HEALTHCHECK":
		if len(fields) > 1 && !strings.EqualFold(fields[1], "NONE") {
			value, p := healthcheck(ins.Text)
			// ponytail: never bound to the api interface; a workload probe, which outranks it, is.
			add("capabilities[health]", value, HealthYAML(p, false))
		}
	case "LABEL":
		for _, tok := range contract.LabelTokens(strings.TrimSpace(ins.Text[len(fields[0]):])) {
			if k, v, _ := strings.Cut(tok, "="); k == "org.opencontainers.image.description" && v != "" {
				add("service.summary", v, summaryYAML(v))
			}
		}
	}
}

// exposedPort reads one EXPOSE argument: 8080, 8080/tcp or 9090/udp.
func exposedPort(tok string) (int, string, bool) {
	num, proto, _ := strings.Cut(tok, "/")
	proto = strings.ToLower(proto)
	if proto == "" {
		proto = "tcp"
	}
	n, err := strconv.Atoi(num)
	return n, proto, err == nil && n > 0 && n < 65536 && (proto == "tcp" || proto == "udp")
}

// localURLRE finds a URL on this container in a HEALTHCHECK command.
var localURLRE = regexp.MustCompile(`https?://(?:[^@/\s]*@)?(?:localhost|127\.0\.0\.1|0\.0\.0\.0|\[::1\])(?::[0-9]+)?[^\s"',\]]*`)

// healthcheck is a HEALTHCHECK's value, "http <path> on <port>" when its
// command calls a URL on this container, else "command", and the path to
// declare ("" when none is valid). The command text and the URL's query are
// never kept: they can hold a token.
func healthcheck(text string) (value, declarePath string) {
	m := localURLRE.FindString(text)
	u, err := url.Parse(cleanURL(m))
	if m == "" || err != nil {
		return "command", ""
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	if contract.ValidCapabilityPath(p) {
		declarePath = p
	}
	return "http " + p + " on " + port, declarePath
}

// summaryRE is the summary pattern of the 2.5 schema: no control character.
var summaryRE = regexp.MustCompile(`^[^\x00-\x1f\x7f-\x9f]+$`)

// summaryYAML declares service.summary, or "" when s is not a valid one.
func summaryYAML(s string) string {
	if utf8.RuneCountInString(s) > 200 || !summaryRE.MatchString(s) {
		return ""
	}
	return "service:\n  summary: " + Quote(s) + "\n"
}
