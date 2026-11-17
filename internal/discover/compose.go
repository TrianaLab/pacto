package discover

import (
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/trianalab/pacto/v4/pkg/contract"
)

// composeFiles are the Compose file names read at the folder root.
var composeFiles = map[string]bool{"compose.yaml": true, "compose.yml": true, "docker-compose.yaml": true, "docker-compose.yml": true}

// compose reads the Compose file p, whose root is c: of the service named
// like this one, else the only one, its image, container ports and
// environment keys. An environment value is never read; include, an extends
// from another file and env_file name files Pacto does not follow.
func (r *reader) compose(p string, c *yaml.Node) {
	if line := keyLine(c, "include"); line > 0 {
		r.find(CodeUnsupportedFile, at(p, line), unsupportedMessage(at(p, line), unsupportedComposeInclude))
	}
	services := contract.MapValue(c, "services")
	var names []string
	for i := 0; services != nil && services.Kind == yaml.MappingNode && i < len(services.Content); i += 2 {
		names = append(names, services.Content[i].Value)
	}
	i, ok := r.pick(at(p, keyLine(c, "services")), "services", names)
	if !ok {
		return
	}
	svc := services.Content[2*i+1]
	if img := contract.MapValue(svc, "image"); scalar(img) != "" {
		r.addAt(p, img.Line, rankCompose, "image", img.Value, "")
	}
	for _, port := range items(contract.MapValue(svc, "ports")) {
		r.composePort(p, port)
	}
	r.composeEnv(p, contract.MapValue(svc, "environment"))
	if line := keyLine(svc, "env_file"); line > 0 {
		r.find(CodeUnsupportedFile, at(p, line), unsupportedMessage(at(p, line), unsupportedComposeEnvFile))
	}
	if line := keyLine(contract.MapValue(svc, "extends"), "file"); line > 0 {
		r.find(CodeUnsupportedFile, at(p, line), unsupportedMessage(at(p, line), unsupportedComposeExtends))
	}
}

// composePort reads one ports entry of the Compose file p: the short form
// ([host-ip:][host:]container[/proto]), whose container side alone is the
// container's, or the long form's target.
func (r *reader) composePort(p string, n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		r.port(p, rankComposePort, contract.MapValue(n, "target"), scalar(contract.MapValue(n, "protocol")))
		return
	}
	spec, proto, _ := strings.Cut(scalar(n), "/")
	r.portAt(p, n.Line, rankComposePort, spec[strings.LastIndexByte(spec, ':')+1:], proto)
}

// composeEnv are the keys of the environment env, a list of KEY=value or KEY
// entries, or a map. A value is never kept: it goes to the Values visitor
// alone. An entry whose key is not a key name (a URL with no '=') is not
// read.
func (r *reader) composeEnv(p string, env *yaml.Node) {
	if env != nil && env.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(env.Content); i += 2 {
			if k := env.Content[i]; valueKeyRE.MatchString(k.Value) {
				r.addKey(p, k.Line, rankCompose, k.Value)
				r.value("compose", p, k.Line, k.Value, scalar(env.Content[i+1]))
			}
		}
	}
	for _, k := range items(env) {
		if name, value, _ := strings.Cut(scalar(k), "="); valueKeyRE.MatchString(name) {
			r.addKey(p, k.Line, rankCompose, name)
			r.value("compose", p, k.Line, name, value)
		}
	}
}
