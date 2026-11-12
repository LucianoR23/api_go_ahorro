package ai

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestLoadPrompt(t *testing.T) {
	fsys := fstest.MapFS{
		"prompts/greet.v3.tmpl":    {Data: []byte("Write for {{.Who}}.")},
		"prompts/greeting.v1.tmpl": {Data: []byte("other")},
		"prompts/twice.v1.tmpl":    {Data: []byte("a")},
		"prompts/twice.v2.tmpl":    {Data: []byte("b")},
		"prompts/broken.v1.tmpl":   {Data: []byte("{{.Missing}}")},
	}
	p, err := loadPrompt(fsys, "greet", map[string]string{"Who": "people"})
	if err != nil || p.System != "Write for people." || p.Version != "greet.v3" {
		t.Fatalf("greet: %+v, %v", p, err)
	}
	for name, want := range map[string]string{
		"twice":   "more than one version",
		"nothing": "no prompt",
		"broken":  "Missing",
	} {
		if _, err := loadPrompt(fsys, name, map[string]string{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err %v, want %q", name, err, want)
		}
	}
}

// Every shipped prompt renders with its own data in the summaries package;
// here, only that the files are well named.
func TestShippedPromptsAreNamed(t *testing.T) {
	entries, err := promptFiles.ReadDir("prompts")
	if err != nil || len(entries) == 0 {
		t.Fatalf("no prompts: %v", err)
	}
	for _, e := range entries {
		if !promptName.MatchString(e.Name()) {
			t.Errorf("%s does not match <name>.v<N>.tmpl", e.Name())
		}
	}
}
