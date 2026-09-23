package manage

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/mudler/nib/types"

	"gopkg.in/yaml.v3"
)

// EndpointInfo is a configured named endpoint in tool-facing form.
type EndpointInfo struct {
	Name     string
	Provider string
	BaseURL  string
	Model    string
}

// userConfigEndpoints reads only the user config file's endpoints list (not the
// merged effective set), so writes never persist plugin-contributed entries.
func userConfigEndpoints(path string) (types.Endpoints, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return types.Endpoints{}, nil
		}
		return nil, err
	}
	var doc struct {
		Endpoints types.Endpoints `yaml:"endpoints"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	return doc.Endpoints, nil
}

// writeUserConfigEndpoints rewrites only the endpoints key, preserving every
// other key (including unknown ones) by round-tripping through a generic map.
func writeUserConfigEndpoints(path string, endpoints types.Endpoints) error {
	root := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return err
		}
	}
	if root == nil {
		root = map[string]any{}
	}
	if len(endpoints) == 0 {
		delete(root, "endpoints")
	} else {
		// Re-encode as a mapping of name -> ModelProviderConfig so the
		// YAML structure matches what UnmarshalYAML expects.
		m := make(map[string]types.ModelProviderConfig, len(endpoints))
		for _, ep := range endpoints {
			m[ep.Name] = ep.ModelProviderConfig
		}
		root["endpoints"] = m
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o644)
}

// ListEndpoints returns the named endpoints configured in the user config file,
// in file order. It reads the writable user file (the authoritative source for
// add/remove), not the merged effective config.
func (c *Configurator) ListEndpoints() ([]EndpointInfo, error) {
	endpoints, err := userConfigEndpoints(c.configPath)
	if err != nil {
		return nil, err
	}
	out := make([]EndpointInfo, 0, len(endpoints))
	for _, ep := range endpoints {
		out = append(out, EndpointInfo{
			Name:     ep.Name,
			Provider: ep.Provider,
			BaseURL:  ep.BaseURL,
			Model:    ep.Model,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AddEndpoint persists a named endpoint to the user config file. If an endpoint
// with the same name already exists it is replaced.
func (c *Configurator) AddEndpoint(name string, cfg types.ModelProviderConfig) error {
	if name == "" {
		return fmt.Errorf("name is required")
	}
	endpoints, err := userConfigEndpoints(c.configPath)
	if err != nil {
		return err
	}
	// Replace if exists, else append.
	replaced := false
	for i, ep := range endpoints {
		if ep.Name == name {
			endpoints[i].ModelProviderConfig = cfg
			replaced = true
			break
		}
	}
	if !replaced {
		endpoints = append(endpoints, types.Endpoint{
			Name:                name,
			ModelProviderConfig: cfg,
		})
	}
	// Validate: surface errors for the whole set so the caller knows if the
	// new entry (or any existing one) was rejected.
	_, errs := endpoints.Validate()
	if len(errs) > 0 {
		var msgs []string
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		return fmt.Errorf("%s", joinErrors(msgs))
	}
	return writeUserConfigEndpoints(c.configPath, endpoints)
}

// RemoveEndpoint deletes a named endpoint from the user config file.
func (c *Configurator) RemoveEndpoint(name string) error {
	endpoints, err := userConfigEndpoints(c.configPath)
	if err != nil {
		return err
	}
	found := false
	out := make(types.Endpoints, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep.Name == name {
			found = true
			continue
		}
		out = append(out, ep)
	}
	if !found {
		return fmt.Errorf("endpoint %q not configured in %s", name, c.configPath)
	}
	return writeUserConfigEndpoints(c.configPath, out)
}

func joinErrors(msgs []string) string {
	out := ""
	for i, m := range msgs {
		if i > 0 {
			out += "; "
		}
		out += m
	}
	return out
}
