// Package config handles pipepie.yaml parsing for `pipepie up`.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// File is the top-level pipepie.yaml structure.
type File struct {
	Server   string            `yaml:"server"`   // tunnel server address
	Key      string            `yaml:"key"`      // server public key (hex)
	Tunnels  map[string]Tunnel `yaml:"tunnels,omitempty"`
	Pipeline *Pipeline         `yaml:"pipeline,omitempty"`
}

// Tunnel defines a single forwarding tunnel.
type Tunnel struct {
	Subdomain string `yaml:"subdomain"`
	Forward   string `yaml:"forward"`
	Port      int    `yaml:"port,omitempty"`
}

// Pipeline defines an async AI webhook pipeline.
type Pipeline struct {
	Name  string `yaml:"name"`
	Steps []Step `yaml:"steps"`
}

// Step is one stage in a webhook pipeline.
type Step struct {
	Name    string `yaml:"name"`              // e.g. "replicate-sdxl"
	Webhook string `yaml:"webhook"`           // path to match, e.g. "/replicate"
	Forward string `yaml:"forward"`           // where to send, e.g. "localhost:3000/on-image"
	Subdomain string `yaml:"subdomain,omitempty"`
}

// Load reads and parses pipepie.yaml from the given path.
func Load(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var f File
	if err := yaml.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := f.validate(); err != nil {
		return nil, err
	}
	return &f, nil
}

// Find searches for pipepie.yaml in the current directory and parents.
func Find() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, "pipepie.yaml")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		path = filepath.Join(dir, "pipepie.yml")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("pipepie.yaml not found (searched up to %s)", dir)
		}
		dir = parent
	}
}

func (f *File) validate() error {
	if f.Server == "" {
		return fmt.Errorf("'server' is required")
	}
	if f.Key == "" {
		return fmt.Errorf("'key' is required (server public key)")
	}
	if len(f.Tunnels) == 0 && f.Pipeline == nil {
		return fmt.Errorf("at least one tunnel or pipeline is required")
	}
	for name, t := range f.Tunnels {
		if t.Subdomain == "" {
			return fmt.Errorf("tunnel %q: 'subdomain' is required", name)
		}
		if t.Forward == "" && t.Port == 0 {
			return fmt.Errorf("tunnel %q: 'forward' or 'port' is required", name)
		}
	}
	if p := f.Pipeline; p != nil {
		if p.Name == "" {
			return fmt.Errorf("pipeline: 'name' is required")
		}
		if len(p.Steps) == 0 {
			return fmt.Errorf("pipeline: at least one step is required")
		}
		for i, s := range p.Steps {
			if s.Name == "" {
				return fmt.Errorf("pipeline step %d: 'name' is required", i)
			}
			if s.Webhook == "" {
				return fmt.Errorf("pipeline step %q: 'webhook' path is required", s.Name)
			}
			if s.Forward == "" {
				return fmt.Errorf("pipeline step %q: 'forward' is required", s.Name)
			}
		}
	}
	return nil
}

// ResolvedTunnels returns all tunnels to connect (including pipeline-derived ones).
func (f *File) ResolvedTunnels() map[string]Tunnel {
	result := make(map[string]Tunnel)

	// Direct tunnels
	for name, t := range f.Tunnels {
		if t.Forward == "" && t.Port > 0 {
			t.Forward = fmt.Sprintf("http://localhost:%d", t.Port)
		}
		result[name] = t
	}

	// Pipeline steps share a subdomain
	if p := f.Pipeline; p != nil {
		for _, s := range p.Steps {
			sub := s.Subdomain
			if sub == "" && len(f.Tunnels) > 0 {
				for _, t := range f.Tunnels {
					sub = t.Subdomain
					break
				}
			}
			if _, exists := result[sub]; !exists && sub != "" {
				result["pipeline-"+s.Name] = Tunnel{
					Subdomain: sub,
					Forward:   s.Forward,
				}
			}
		}
	}

	return result
}
