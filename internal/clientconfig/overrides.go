// Package clientconfig holds the per-(frontend, service) endpoint-
// override knobs that `shimctl env` emits. The YAML next to this
// file is the single source of truth.
package clientconfig

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed overrides.yaml
var overridesYAML []byte

// Service captures the endpoint-override knobs for one (frontend
// cloud, shimmed service) pair.
type Service struct {
	Env       map[string]string `yaml:"env"`
	Terraform TerraformBlock    `yaml:"terraform"`
	SDK       string            `yaml:"sdk"`
	CLI       string            `yaml:"cli"`
}

type TerraformBlock struct {
	Provider       string `yaml:"provider"`
	EndpointsBlock string `yaml:"endpoints_block"`
}

type Overrides struct {
	Frontends map[string]struct {
		Services map[string]Service `yaml:"services"`
	} `yaml:"frontends"`
}

// Load parses the embedded overrides.yaml.
func Load() (*Overrides, error) {
	var out Overrides
	if err := yaml.Unmarshal(overridesYAML, &out); err != nil {
		return nil, fmt.Errorf("parse overrides.yaml: %w", err)
	}
	return &out, nil
}

// FrontendList returns the sorted list of frontends defined in the YAML.
func (o *Overrides) FrontendList() []string {
	keys := make([]string, 0, len(o.Frontends))
	for k := range o.Frontends {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Services returns the sorted list of services for the given
// frontend, or nil if the frontend isn't defined.
func (o *Overrides) Services(frontend string) []string {
	f, ok := o.Frontends[frontend]
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(f.Services))
	for k := range f.Services {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Lookup returns the Service entry for one (frontend, service) pair.
func (o *Overrides) Lookup(frontend, service string) (Service, error) {
	f, ok := o.Frontends[frontend]
	if !ok {
		return Service{}, fmt.Errorf("no such frontend %q (valid: %s)", frontend, strings.Join(o.FrontendList(), ", "))
	}
	s, ok := f.Services[service]
	if !ok {
		return Service{}, fmt.Errorf("no such service %q for frontend %q (valid: %s)",
			service, frontend, strings.Join(o.Services(frontend), ", "))
	}
	return s, nil
}

// RenderShell renders the env knobs for one Service as POSIX
// `export` lines with $SHIM substituted.
func (s Service) RenderShell(shim string) string {
	keys := make([]string, 0, len(s.Env))
	for k := range s.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		v := strings.ReplaceAll(s.Env[k], "$SHIM", shim)
		fmt.Fprintf(&sb, "export %s=%q\n", k, v)
	}
	return sb.String()
}

// RenderTerraform renders the Terraform endpoints block for one
// Service with $SHIM substituted. Returns "" when no block is set.
func (s Service) RenderTerraform(shim string) string {
	if s.Terraform.EndpointsBlock == "" {
		return ""
	}
	return strings.ReplaceAll(s.Terraform.EndpointsBlock, "$SHIM", shim)
}
