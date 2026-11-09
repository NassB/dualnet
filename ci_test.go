package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The release pipeline keeps its safety properties: nothing can quietly become
// less strict (an unpinned action, write access for every job, an old Go).
func TestWorkflowHygiene(t *testing.T) {
	data, err := os.ReadFile(".github/workflows/build.yml")
	if err != nil {
		t.Fatal(err)
	}
	var wf struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			If          string            `yaml:"if"`
			Needs       any               `yaml:"needs"`
			Permissions map[string]string `yaml:"permissions"`
			Steps       []struct {
				Name string            `yaml:"name"`
				Uses string            `yaml:"uses"`
				With map[string]any    `yaml:"with"`
				Run  string            `yaml:"run"`
				Env  map[string]string `yaml:"env"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &wf); err != nil {
		t.Fatalf("build.yml is not valid YAML: %v", err)
	}

	// read-only by default; only the release job may write, attest or sign
	if len(wf.Permissions) != 1 || wf.Permissions["contents"] != "read" {
		t.Errorf("workflow permissions = %v, want only contents: read", wf.Permissions)
	}
	for name, job := range wf.Jobs {
		if name != "release" && len(job.Permissions) > 0 {
			t.Errorf("job %q has its own permissions %v: only the release job should", name, job.Permissions)
		}
	}
	rel, ok := wf.Jobs["release"]
	if !ok {
		t.Fatal("no release job")
	}
	if !strings.Contains(rel.If, "refs/tags/v") {
		t.Errorf("release job runs on %q: it must only run for version tags", rel.If)
	}
	needs, ok := rel.Needs.([]any)
	if !ok || len(needs) != 2 || needs[0] != "build" || needs[1] != "unix-test" {
		t.Errorf("release needs %v, want build and unix-test", rel.Needs)
	}
	for _, p := range []string{"contents", "id-token", "attestations"} {
		if rel.Permissions[p] != "write" {
			t.Errorf("release job lacks %s: write", p)
		}
	}

	// every action pinned to a full commit
	pinned := regexp.MustCompile(`^[\w.-]+/[\w.-]+@[0-9a-f]{40}$`)
	uses := 0
	for name, job := range wf.Jobs {
		for _, st := range job.Steps {
			if st.Uses == "" {
				continue
			}
			uses++
			if !pinned.MatchString(st.Uses) {
				t.Errorf("%s: %q is not pinned to a commit SHA", name, st.Uses)
			}
		}
	}
	if uses < 5 {
		t.Errorf("only %d actions found: the scan is broken", uses)
	}

	// the build uses the newest Go, and checks vulnerabilities and the checksum
	build := wf.Jobs["build"]
	var sawGo, sawVuln, sawSum bool
	for _, st := range build.Steps {
		if strings.HasPrefix(st.Uses, "actions/setup-go@") {
			sawGo = st.With["go-version"] == "stable" && st.With["check-latest"] == true
		}
		sawVuln = sawVuln || strings.Contains(st.Run, "govulncheck")
		sawSum = sawSum || strings.Contains(st.Run, "sha256sum")
	}
	if !sawGo {
		t.Error("the build must use go-version: stable with check-latest (not the Go version of go.mod)")
	}
	if !sawVuln || !sawSum {
		t.Errorf("build lacks govulncheck (%v) or the checksum (%v)", sawVuln, sawSum)
	}
}

// A typo in a workflow, an issue form or the Dependabot file would only show up
// on GitHub: make sure they at least parse.
func TestGitHubYAMLParses(t *testing.T) {
	var files []string
	err := filepath.WalkDir(".github", func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && (strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".yaml")) {
			files = append(files, p)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("only %d YAML files under .github: %v", len(files), files)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
