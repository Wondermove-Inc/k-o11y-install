package embed

import (
	"embed"
	"fmt"
)

//go:embed templates/*
var TemplatesFS embed.FS

// ReadTemplate reads a YAML template from embedded resources.
func ReadTemplate(name string) ([]byte, error) {
	data, err := TemplatesFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("embedded template %s not found: %w", name, err)
	}
	return data, nil
}
