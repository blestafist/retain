package llm

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"path"
	"strings"
	"text/template"
)

//go:embed prompts/*.tmpl
var promptFiles embed.FS

// RenderPrompt executes a trusted embedded prompt template by filename.
func RenderPrompt(name string, data any) (string, error) {
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || path.Base(name) != name || !strings.HasSuffix(name, ".tmpl") {
		return "", errors.New("invalid llm prompt name")
	}
	contents, err := promptFiles.ReadFile(path.Join("prompts", name))
	if err != nil {
		return "", fmt.Errorf("read llm prompt: %w", err)
	}
	tmpl, err := template.New(name).Option("missingkey=error").Parse(string(contents))
	if err != nil {
		return "", fmt.Errorf("parse llm prompt: %w", err)
	}
	var rendered bytes.Buffer
	if err := tmpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("render llm prompt: %w", err)
	}
	return rendered.String(), nil
}
