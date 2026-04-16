// Package embed provides access to embedded SQL, scripts, and binaries.
package embed

import (
	"embed"
	"fmt"
	"os"
)

//go:embed sql/* scripts/* bin/* templates/*
var EmbeddedFS embed.FS

// ReadSQL reads a SQL file from embedded resources.
func ReadSQL(name string) ([]byte, error) {
	data, err := EmbeddedFS.ReadFile("sql/" + name)
	if err != nil {
		return nil, fmt.Errorf("embedded SQL %s not found: %w", name, err)
	}
	return data, nil
}

// ReadScript reads a script file from embedded resources.
func ReadScript(name string) ([]byte, error) {
	data, err := EmbeddedFS.ReadFile("scripts/" + name)
	if err != nil {
		return nil, fmt.Errorf("embedded script %s not found: %w", name, err)
	}
	return data, nil
}

// ExtractBinary extracts an embedded binary to a temp file and returns the path.
// Caller must call cleanup() to remove the temp file.
func ExtractBinary(name string) (path string, cleanup func(), err error) {
	data, err := EmbeddedFS.ReadFile("bin/" + name)
	if err != nil {
		return "", nil, fmt.Errorf("embedded binary %s not found: %w", name, err)
	}

	tmpFile, err := os.CreateTemp("", "k-o11y-embed-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temp file: %w", err)
	}

	if _, err := tmpFile.Write(data); err != nil {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("write temp file: %w", err)
	}
	tmpFile.Close()

	if err := os.Chmod(tmpFile.Name(), 0755); err != nil {
		os.Remove(tmpFile.Name())
		return "", nil, fmt.Errorf("chmod temp file: %w", err)
	}

	return tmpFile.Name(), func() { os.Remove(tmpFile.Name()) }, nil
}

// ReadBin reads a binary file from embedded resources.
func ReadBin(name string) ([]byte, error) {
	data, err := EmbeddedFS.ReadFile("bin/" + name)
	if err != nil {
		return nil, fmt.Errorf("embedded binary %s not found: %w", name, err)
	}
	return data, nil
}

// ReadTemplate reads a template file from embedded resources.
func ReadTemplate(name string) ([]byte, error) {
	data, err := EmbeddedFS.ReadFile("templates/" + name)
	if err != nil {
		return nil, fmt.Errorf("embedded template %s not found: %w", name, err)
	}
	return data, nil
}

// ListSQL returns all embedded SQL file names.
func ListSQL() ([]string, error) {
	entries, err := EmbeddedFS.ReadDir("sql")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}
