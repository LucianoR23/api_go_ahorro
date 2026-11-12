package ai

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"text/template"
)

// Prompt templates live in prompts/<name>.v<N>.tmpl, one version per name
// at a time. A change to a prompt bumps N in the file name, so ai_usage
// tells which prompt produced each call (plan 8.1, 8.2).
//
//go:embed prompts/*.tmpl
var promptFiles embed.FS

// Prompt is a rendered system prompt and the version stored with its calls.
type Prompt struct {
	System  string
	Version string // "weekly_summary.v1"
}

var promptName = regexp.MustCompile(`^([a-z_]+)\.(v[0-9]+)\.tmpl$`)

// LoadPrompt renders the template named name with data. It fails when no
// file or more than one file has that name.
func LoadPrompt(name string, data any) (Prompt, error) {
	return loadPrompt(promptFiles, name, data)
}

func loadPrompt(fsys fs.FS, name string, data any) (Prompt, error) {
	matches, err := fs.Glob(fsys, "prompts/"+name+".v*.tmpl")
	if err != nil {
		return Prompt{}, err
	}
	var file, version string
	for _, m := range matches {
		parts := promptName.FindStringSubmatch(path.Base(m))
		if parts == nil || parts[1] != name {
			continue
		}
		if file != "" {
			return Prompt{}, fmt.Errorf("ai: prompt %q has more than one version", name)
		}
		file, version = m, parts[2]
	}
	if file == "" {
		return Prompt{}, fmt.Errorf("ai: no prompt %q", name)
	}
	tpl, err := template.New(path.Base(file)).Option("missingkey=error").ParseFS(fsys, file)
	if err != nil {
		return Prompt{}, fmt.Errorf("ai: prompt %q: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return Prompt{}, fmt.Errorf("ai: prompt %q: %w", name, err)
	}
	return Prompt{System: buf.String(), Version: name + "." + version}, nil
}

// MustPrompt is LoadPrompt for prompts fixed at build time.
func MustPrompt(name string, data any) Prompt {
	p, err := LoadPrompt(name, data)
	if err != nil {
		panic(err)
	}
	return p
}
