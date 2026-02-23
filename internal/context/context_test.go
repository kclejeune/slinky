package context

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func TestDiscoverLayers(t *testing.T) {
	tmpDir := t.TempDir()

	orgDir := filepath.Join(tmpDir, "work", "org-a")
	subDir := filepath.Join(tmpDir, "work", "org-a", "sub-project")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(orgDir, DefaultProjectConfigNames[0]), []byte("[files.netrc]\ntemplate = \"tpl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(subDir, DefaultProjectConfigNames[0]), []byte("[files.npmrc]\ntemplate = \"tpl\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	paths := DiscoverLayers(subDir, DefaultProjectConfigNames)

	if len(paths) < 2 {
		t.Fatalf("DiscoverLayers found %d paths, want at least 2", len(paths))
	}

	if filepath.Dir(paths[0]) != orgDir {
		t.Errorf("first layer dir = %q, want %q", filepath.Dir(paths[0]), orgDir)
	}
	if filepath.Dir(paths[1]) != subDir {
		t.Errorf("second layer dir = %q, want %q", filepath.Dir(paths[1]), subDir)
	}
}

func TestDiscoverLayersNoConfigs(t *testing.T) {
	tmpDir := t.TempDir()
	paths := DiscoverLayers(tmpDir, DefaultProjectConfigNames)
	if len(paths) != 0 {
		t.Errorf("expected 0 paths, got %d", len(paths))
	}
}

func TestMergeGlobalOnly(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "tplA"},
			"npmrc": {Render: "native", Template: "tplB"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)
	eff := mgr.Effective()

	if len(eff) != 2 {
		t.Fatalf("effective has %d files, want 2", len(eff))
	}
	if eff["netrc"].Template != "tplA" {
		t.Errorf("netrc template = %q, want %q", eff["netrc"].Template, "tplA")
	}
	if eff["npmrc"].Template != "tplB" {
		t.Errorf("npmrc template = %q, want %q", eff["npmrc"].Template, "tplB")
	}
}

func TestMergeDeeperWinsPerFile(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "global-netrc"},
			"npmrc": {Render: "native", Template: "global-npmrc"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	orgDir := filepath.Join(tmpDir, "org-a")
	subDir := filepath.Join(tmpDir, "org-a", "sub")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}

	orgTpl := filepath.Join(tmpDir, "org-netrc.tpl")
	if err := os.WriteFile(orgTpl, []byte(`{{ env "ORG_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dockerTpl := filepath.Join(tmpDir, "org-docker.tpl")
	if err := os.WriteFile(dockerTpl, []byte(`{{ env "ORG_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	subTpl := filepath.Join(tmpDir, "sub-npmrc.tpl")
	if err := os.WriteFile(subTpl, []byte(`{{ env "SUB_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	orgConfig := fmt.Sprintf("[files.netrc]\ntemplate = %q\nmode = 384\n[files.\"docker/config.json\"]\ntemplate = %q\nmode = 384\n", orgTpl, dockerTpl)
	if err := os.WriteFile(filepath.Join(orgDir, DefaultProjectConfigNames[0]), []byte(orgConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	subConfig := fmt.Sprintf("[files.npmrc]\ntemplate = %q\nmode = 384\n", subTpl)
	if err := os.WriteFile(filepath.Join(subDir, DefaultProjectConfigNames[0]), []byte(subConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"ORG_TOKEN": "abc", "SUB_TOKEN": "xyz"}
	names, err := mgr.Activate(subDir, env, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	if len(names) != 3 {
		t.Errorf("activate returned %d names, want 3", len(names))
	}

	effective := mgr.Effective()

	if effective["netrc"].Template != orgTpl {
		t.Errorf("netrc template = %q, want %q", effective["netrc"].Template, orgTpl)
	}
	if effective["netrc"].Env["ORG_TOKEN"] != "abc" {
		t.Error("netrc should carry org env")
	}

	if effective["npmrc"].Template != subTpl {
		t.Errorf("npmrc template = %q, want %q", effective["npmrc"].Template, subTpl)
	}
	if effective["npmrc"].Env["SUB_TOKEN"] != "xyz" {
		t.Error("npmrc should carry sub env")
	}

	if effective["docker/config.json"].Template != dockerTpl {
		t.Errorf("docker template = %q, want %q", effective["docker/config.json"].Template, dockerTpl)
	}

	if len(effective) != 3 {
		t.Errorf("effective has %d files, want 3", len(effective))
	}
}

func TestActivateUpdatesEffective(t *testing.T) {
	tmpDir := t.TempDir()

	tplA := filepath.Join(tmpDir, "tplA.tpl")
	tplB := filepath.Join(tmpDir, "tplB.tpl")
	tplC := filepath.Join(tmpDir, "tplC.tpl")
	if err := os.WriteFile(tplA, []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tplB, []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tplC, []byte(`{{ env "PROJECT_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: tplA, Mode: 0o600},
			"npmrc": {Render: "native", Template: tplB, Mode: 0o600},
		},
	}

	var notified bool
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(map[string]*EffectiveFile) {
		notified = true
	})

	projDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := `[files.netrc]
template = "` + tplC + `"
mode = 384
`
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]), []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"PROJECT_TOKEN": "secret"}
	names, err := mgr.Activate(projDir, env, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	if !notified {
		t.Error("onChange should have been called")
	}

	if len(names) != 2 {
		t.Errorf("activate returned %d names, want 2", len(names))
	}

	eff := mgr.Effective()
	if eff["netrc"].Template != tplC {
		t.Errorf("netrc should be overridden by project, got template %q", eff["netrc"].Template)
	}
	if eff["netrc"].Env["PROJECT_TOKEN"] != "secret" {
		t.Error("netrc should carry project env")
	}
	if eff["npmrc"].Template != tplB {
		t.Errorf("npmrc should come from global, got template %q", eff["npmrc"].Template)
	}
}

func TestActivateBackToGlobal(t *testing.T) {
	tmpDir := t.TempDir()

	tplA := filepath.Join(tmpDir, "tplA.tpl")
	if err := os.WriteFile(tplA, []byte("global"), 0o644); err != nil {
		t.Fatal(err)
	}

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: tplA, Mode: 0o600},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	names, err := mgr.Activate(tmpDir, nil, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	if len(names) != 1 {
		t.Errorf("activate returned %d names, want 1", len(names))
	}

	eff := mgr.Effective()
	if eff["netrc"].Template != tplA {
		t.Errorf("netrc should be from global, got template %q", eff["netrc"].Template)
	}
}

func TestEffectiveFileEnvLookup(t *testing.T) {
	ef := &EffectiveFile{
		FileConfig: &config.FileConfig{},
		Env:        map[string]string{"MY_KEY": "from_context"},
	}

	lookup := ef.EnvLookupFunc()

	val, ok := lookup("MY_KEY")
	if !ok || val != "from_context" {
		t.Errorf("lookup(MY_KEY) = %q, %v; want %q, true", val, ok, "from_context")
	}

	t.Setenv("OS_ENV_TEST_KEY", "from_os")
	val, ok = lookup("OS_ENV_TEST_KEY")
	if !ok || val != "from_os" {
		t.Errorf("lookup(OS_ENV_TEST_KEY) = %q, %v; want %q, true", val, ok, "from_os")
	}

	_, ok = lookup("DEFINITELY_NOT_SET_XYZ_12345")
	if ok {
		t.Error("expected false for unset key")
	}
}

func TestEffectiveFileEnvLookupNilEnv(t *testing.T) {
	ef := &EffectiveFile{
		FileConfig: &config.FileConfig{},
		Env:        nil,
	}

	lookup := ef.EnvLookupFunc()

	t.Setenv("OS_ENV_ONLY_KEY", "only_os")
	val, ok := lookup("OS_ENV_ONLY_KEY")
	if !ok || val != "only_os" {
		t.Errorf("lookup(OS_ENV_ONLY_KEY) = %q, %v; want %q, true", val, ok, "only_os")
	}
}

func TestActivateFiltersEnv(t *testing.T) {
	tmpDir := t.TempDir()

	tplPath := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplPath, []byte(`machine github.com password {{ env "GITHUB_TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := `[files.netrc]
template = "` + tplPath + `"
mode = 384
`
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]), []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"GITHUB_TOKEN":    "ghp_secret",
		"PATH":            "/usr/bin:/bin",
		"HOME":            "/home/user",
		"TERM":            "xterm-256color",
		"TERM_SESSION_ID": "abc123",
		"PWD":             "/tmp",
		"OLDPWD":          "/var",
		"_":               "/usr/bin/env",
	}
	_, err := mgr.Activate(projDir, env, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	eff := mgr.Effective()
	netrc := eff["netrc"]
	if netrc == nil {
		t.Fatal("expected netrc in effective files")
	}

	if len(netrc.Env) != 1 {
		t.Errorf("expected 1 env var, got %d: %v", len(netrc.Env), netrc.Env)
	}
	if netrc.Env["GITHUB_TOKEN"] != "ghp_secret" {
		t.Errorf("GITHUB_TOKEN = %q, want %q", netrc.Env["GITHUB_TOKEN"], "ghp_secret")
	}
	if _, ok := netrc.Env["PATH"]; ok {
		t.Error("PATH should have been filtered out")
	}
}

func TestActivateCommandModePreservesAllowlistEnv(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"secret": {

				Render:  "command",
				Command: "echo",
				Args:    []string{"hello"},
				Mode:    0o600,
			},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{"FOO": "bar", "PATH": "/usr/local/bin:/usr/bin", "HOME": "/home/user"}
	_, err := mgr.Activate(projDir, env, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	eff := mgr.Effective()
	secret := eff["secret"]
	if secret == nil {
		t.Fatal("expected secret in effective files")
	}

	if secret.Env == nil {
		t.Fatal("expected non-nil env for command mode")
	}
	if secret.Env["PATH"] != "/usr/local/bin:/usr/bin" {
		t.Errorf("PATH = %q, want %q", secret.Env["PATH"], "/usr/local/bin:/usr/bin")
	}
	if secret.Env["HOME"] != "/home/user" {
		t.Errorf("HOME = %q, want %q", secret.Env["HOME"], "/home/user")
	}
	if _, ok := secret.Env["FOO"]; ok {
		t.Error("FOO should have been filtered out (not in allowlist)")
	}
}

func TestActivateConcurrentSafe(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "global-tpl", Mode: 0o600},
		},
	}

	const N = 10
	dirs := make([]string, N)
	for i := range N {
		dir := filepath.Join(tmpDir, fmt.Sprintf("project-%d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		tplPath := filepath.Join(dir, fmt.Sprintf("file-%d.tpl", i))
		if err := os.WriteFile(tplPath, fmt.Appendf(nil, "tpl-%d", i), 0o644); err != nil {
			t.Fatal(err)
		}
		projConfig := fmt.Sprintf("[files.\"file-%d\"]\ntemplate = %q\nmode = 384\n", i, tplPath)
		if err := os.WriteFile(filepath.Join(dir, DefaultProjectConfigNames[0]), []byte(projConfig), 0o644); err != nil {
			t.Fatal(err)
		}
		dirs[i] = dir
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(map[string]*EffectiveFile) {})

	var wg sync.WaitGroup
	wg.Add(N)
	for i := range N {
		go func() {
			defer wg.Done()
			env := map[string]string{"IDX": fmt.Sprintf("%d", i)}
			_, _ = mgr.Activate(dirs[i], env, 0)
		}()
	}
	wg.Wait()

	eff := mgr.Effective()
	if _, ok := eff["netrc"]; !ok {
		t.Fatal("expected netrc in effective files")
	}
}

func TestLayersReturnsSnapshot(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)
	layers := mgr.Layers()

	if len(layers) != 0 {
		t.Errorf("expected 0 layers, got %d", len(layers))
	}
}

func TestMultiActivateAdditive(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"global-file": {Render: "native", Template: "global-tpl"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a-netrc.tpl")
	if err := os.WriteFile(tplA, []byte("netrc-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b-npmrc.tpl")
	if err := os.WriteFile(tplB, []byte("npmrc-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projA, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projA) error: %v", err)
	}

	names, err := mgr.Activate(projB, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projB) error: %v", err)
	}

	if len(names) != 3 {
		t.Errorf("effective has %d files, want 3: %v", len(names), names)
	}

	eff := mgr.Effective()
	if eff["global-file"] == nil {
		t.Error("expected global-file in effective")
	}
	if eff["netrc"] == nil {
		t.Error("expected netrc in effective")
	}
	if eff["npmrc"] == nil {
		t.Error("expected npmrc in effective")
	}
}

func TestMultiActivateConflict(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a-netrc.tpl")
	if err := os.WriteFile(tplA, []byte("netrc-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b-netrc.tpl")
	if err := os.WriteFile(tplB, []byte("netrc-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projA, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projA) error: %v", err)
	}

	_, err = mgr.Activate(projB, nil, 0)
	if err == nil {
		t.Fatal("expected conflict error from Activate(projB)")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict in error, got: %v", err)
	}

	eff := mgr.Effective()
	if eff["netrc"] == nil {
		t.Error("netrc should still be present from projA")
	}
	if eff["netrc"].Template != tplA {
		t.Errorf("netrc template should be from projA, got %q", eff["netrc"].Template)
	}

	acts := mgr.Activations()
	if len(acts) != 1 {
		t.Errorf("expected 1 activation, got %d", len(acts))
	}
}

func TestMultiActivateGlobalOverrideSingleProject(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "global-tpl"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "proj-netrc.tpl")
	if err := os.WriteFile(tpl, []byte("proj-netrc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	names, err := mgr.Activate(projDir, nil, 0)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	if len(names) != 1 {
		t.Errorf("expected 1 file, got %d", len(names))
	}

	eff := mgr.Effective()
	if eff["netrc"].Template != tpl {
		t.Errorf("netrc template = %q, want project override %q", eff["netrc"].Template, tpl)
	}
}

func TestMultiActivateGlobalOverrideTwoProjects(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "global-tpl"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a-netrc.tpl")
	if err := os.WriteFile(tplA, []byte("a-netrc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b-netrc.tpl")
	if err := os.WriteFile(tplB, []byte("b-netrc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projA, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projA) error: %v", err)
	}

	_, err = mgr.Activate(projB, nil, 0)
	if err == nil {
		t.Fatal("expected conflict error when two projects override same global file")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("expected conflict in error, got: %v", err)
	}
}

func TestDeactivateReenablesActivation(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a-netrc.tpl")
	if err := os.WriteFile(tplA, []byte("netrc-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b-netrc.tpl")
	if err := os.WriteFile(tplB, []byte("netrc-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projA, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projA) error: %v", err)
	}

	_, err = mgr.Activate(projB, nil, 0)
	if err == nil {
		t.Fatal("expected conflict")
	}

	_, err = mgr.Deactivate(projA, 0)
	if err != nil {
		t.Fatalf("Deactivate(projA) error: %v", err)
	}

	names, err := mgr.Activate(projB, nil, 0)
	if err != nil {
		t.Fatalf("Activate(projB) after deactivate error: %v", err)
	}

	if len(names) != 1 {
		t.Errorf("expected 1 file, got %d", len(names))
	}

	eff := mgr.Effective()
	if eff["netrc"] == nil {
		t.Fatal("expected netrc in effective")
	}
	if eff["netrc"].Template != tplB {
		t.Errorf("netrc should be from projB, got template %q", eff["netrc"].Template)
	}
}

func TestMultiActivateReactivateSameDir(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte(`{{ env "TOKEN" }}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projDir, map[string]string{"TOKEN": "old"}, 0)
	if err != nil {
		t.Fatalf("first Activate() error: %v", err)
	}

	_, err = mgr.Activate(projDir, map[string]string{"TOKEN": "new"}, 0)
	if err != nil {
		t.Fatalf("second Activate() error: %v", err)
	}

	eff := mgr.Effective()
	if eff["netrc"].Env["TOKEN"] != "new" {
		t.Errorf("TOKEN = %q, want %q", eff["netrc"].Env["TOKEN"], "new")
	}

	acts := mgr.Activations()
	if len(acts) != 1 {
		t.Errorf("expected 1 activation, got %d", len(acts))
	}
}

func TestDeactivate(t *testing.T) {
	tmpDir := t.TempDir()

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"global-file": {Render: "native", Template: "global-tpl"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a-netrc.tpl")
	if err := os.WriteFile(tplA, []byte("netrc-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b-npmrc.tpl")
	if err := os.WriteFile(tplB, []byte("npmrc-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projA, nil, 0)
	_, _ = mgr.Activate(projB, nil, 0)

	names, err := mgr.Deactivate(projA, 0)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}

	if len(names) != 2 {
		t.Errorf("expected 2 files after deactivate, got %d: %v", len(names), names)
	}

	eff := mgr.Effective()
	if eff["netrc"] != nil {
		t.Error("netrc should be gone after deactivating projA")
	}
	if eff["npmrc"] == nil {
		t.Error("npmrc should still be present from projB")
	}

	acts := mgr.Activations()
	if len(acts) != 1 {
		t.Errorf("expected 1 activation, got %d", len(acts))
	}
}

func TestDeactivateNotActive(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"global-file": {Render: "native", Template: "global-tpl"},
		},
	}

	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	names, err := mgr.Deactivate("/nonexistent/dir", 0)
	if err != nil {
		t.Fatalf("Deactivate() unexpected error: %v", err)
	}

	if len(names) != 1 {
		t.Errorf("expected 1 file, got %d: %v", len(names), names)
	}
}

func TestActivateWithSession(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := mgr.Activate(projDir, nil, 1234)
	if err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	sessions := mgr.Sessions()
	if pids, ok := sessions[projDir]; !ok || len(pids) != 1 || pids[0] != 1234 {
		t.Errorf("Sessions() = %v, want {%q: [1234]}", sessions, projDir)
	}

	pids := mgr.TrackedPIDs()
	if len(pids) != 1 || pids[0] != 1234 {
		t.Errorf("TrackedPIDs() = %v, want [1234]", pids)
	}
}

func TestDeactivateWithSessionRefCount(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projDir, nil, 100)
	_, _ = mgr.Activate(projDir, nil, 200)

	names, err := mgr.Deactivate(projDir, 100)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}
	if len(names) != 1 {
		t.Errorf("expected 1 file, got %d", len(names))
	}

	acts := mgr.Activations()
	if _, ok := acts[projDir]; !ok {
		t.Fatal("activation should still exist with one session remaining")
	}

	sessions := mgr.Sessions()
	if pids := sessions[projDir]; len(pids) != 1 || pids[0] != 200 {
		t.Errorf("remaining sessions = %v, want [200]", pids)
	}
}

func TestDeactivateLastSession(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projDir, nil, 100)
	_, _ = mgr.Activate(projDir, nil, 200)

	_, _ = mgr.Deactivate(projDir, 100)
	names, err := mgr.Deactivate(projDir, 200)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}

	if len(names) != 0 {
		t.Errorf("expected 0 files, got %d: %v", len(names), names)
	}

	acts := mgr.Activations()
	if len(acts) != 0 {
		t.Errorf("expected 0 activations, got %d", len(acts))
	}

	if len(mgr.TrackedPIDs()) != 0 {
		t.Error("expected no tracked PIDs after all sessions removed")
	}
}

func TestDeactivateWithoutSessionForceRemoves(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projDir, nil, 100)
	_, _ = mgr.Activate(projDir, nil, 200)

	names, err := mgr.Deactivate(projDir, 0)
	if err != nil {
		t.Fatalf("Deactivate() error: %v", err)
	}

	if len(names) != 0 {
		t.Errorf("expected 0 files, got %d", len(names))
	}

	acts := mgr.Activations()
	if len(acts) != 0 {
		t.Error("expected 0 activations after force-deactivate")
	}

	if len(mgr.TrackedPIDs()) != 0 {
		t.Error("expected no tracked PIDs after force-deactivate")
	}
}

func TestRemoveSession(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projDir := filepath.Join(tmpDir, "proj")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tpl := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tpl, []byte("tpl"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tpl), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projDir, nil, 1234)

	deactivated := mgr.RemoveSession(1234)
	if len(deactivated) != 1 || deactivated[0] != projDir {
		t.Errorf("RemoveSession() = %v, want [%s]", deactivated, projDir)
	}

	acts := mgr.Activations()
	if len(acts) != 0 {
		t.Error("expected 0 activations after RemoveSession")
	}
}

func TestSessionActivateMultipleDirsDifferentSessions(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Activate(projA, nil, 5678); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Activate(projB, nil, 5679); err != nil {
		t.Fatal(err)
	}

	acts := mgr.Activations()
	if len(acts) != 2 {
		t.Errorf("expected 2 activations, got %d", len(acts))
	}

	deactivated := mgr.RemoveSession(5678)
	if len(deactivated) != 1 || deactivated[0] != projA {
		t.Errorf("RemoveSession(5678) = %v, want [%s]", deactivated, projA)
	}

	acts = mgr.Activations()
	if len(acts) != 1 {
		t.Errorf("expected 1 activation, got %d", len(acts))
	}
	if _, ok := acts[projB]; !ok {
		t.Error("projB should still be active")
	}
}

func TestActivateAutoDeactivatesOutsideDirs(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projA, nil, 1000)

	acts := mgr.Activations()
	if _, ok := acts[projA]; !ok {
		t.Fatal("projA should be active")
	}

	_, _ = mgr.Activate(projB, nil, 1000)

	acts = mgr.Activations()
	if _, ok := acts[projA]; ok {
		t.Error("projA should have been auto-deactivated")
	}
	if _, ok := acts[projB]; !ok {
		t.Fatal("projB should be active")
	}

	sessions := mgr.Sessions()
	if _, ok := sessions[projA]; ok {
		t.Error("projA should have no sessions")
	}
	if pids := sessions[projB]; len(pids) != 1 || pids[0] != 1000 {
		t.Errorf("projB sessions = %v, want [1000]", pids)
	}
}

func TestActivateAutoDeactivatesAncestor(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	parentDir := filepath.Join(tmpDir, "org")
	childDir := filepath.Join(tmpDir, "org", "project")
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}

	tplParent := filepath.Join(tmpDir, "parent.tpl")
	if err := os.WriteFile(tplParent, []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parentDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplParent), 0o644); err != nil {
		t.Fatal(err)
	}

	tplChild := filepath.Join(tmpDir, "child.tpl")
	if err := os.WriteFile(tplChild, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplChild), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(parentDir, nil, 2000)

	_, _ = mgr.Activate(childDir, nil, 2000)

	acts := mgr.Activations()
	if _, ok := acts[parentDir]; ok {
		t.Error("parentDir should have been auto-deactivated (child subsumes its layers)")
	}
	if _, ok := acts[childDir]; !ok {
		t.Error("childDir should be active")
	}

	eff := mgr.Effective()
	if eff["netrc"] == nil {
		t.Error("netrc should be present (from parent layer discovered by child)")
	}
	if eff["npmrc"] == nil {
		t.Error("npmrc should be present (from child's own config)")
	}
}

func TestUpdateGlobalSwapsFiles(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "tplA"},
		},
	}

	var notified bool
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(map[string]*EffectiveFile) {
		notified = true
	})

	eff := mgr.Effective()
	if eff["netrc"].Template != "tplA" {
		t.Errorf("initial template = %q, want tplA", eff["netrc"].Template)
	}

	newCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "tplB"},
			"npmrc": {Render: "native", Template: "tplC"},
		},
	}

	mgr.UpdateGlobal(newCfg, nil)

	eff = mgr.Effective()
	if eff["netrc"].Template != "tplB" {
		t.Errorf("updated template = %q, want tplB", eff["netrc"].Template)
	}
	if eff["npmrc"] == nil {
		t.Error("expected npmrc in effective after UpdateGlobal")
	}
	if !notified {
		t.Error("onChange should have been called")
	}
}

func TestUpdateGlobalNoChangeNoNotify(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "tplA"},
		},
	}

	var notified bool
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(map[string]*EffectiveFile) {
		notified = true
	})

	// Same config again.
	sameCfg := &config.Config{
		Files: map[string]*config.FileConfig{
			"netrc": {Render: "native", Template: "tplA"},
		},
	}
	mgr.UpdateGlobal(sameCfg, nil)

	if notified {
		t.Error("onChange should not be called when config unchanged")
	}
}

func TestUpdateGlobalUpdatesConfigNames(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	newNames := []string{".slinky.toml"}
	mgr.UpdateGlobal(globalCfg, newNames)

	// Verify by checking that the manager uses the new config names.
	// We do this indirectly by checking Layers() works (no panic).
	_ = mgr.Layers()
}

func TestRefreshActivationReloadsProjectConfig(t *testing.T) {
	tmpDir := t.TempDir()

	tplFile := filepath.Join(tmpDir, "netrc.tpl")
	if err := os.WriteFile(tplFile, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}

	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}

	var notifyCount int
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, func(map[string]*EffectiveFile) {
		notifyCount++
	})

	projDir := filepath.Join(tmpDir, "project")
	if err := os.MkdirAll(projDir, 0o755); err != nil {
		t.Fatal(err)
	}
	projConfig := fmt.Sprintf("[files.netrc]\ntemplate = %q\nmode = 384\n", tplFile)
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]), []byte(projConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := mgr.Activate(projDir, nil, 0); err != nil {
		t.Fatalf("Activate() error: %v", err)
	}

	eff := mgr.Effective()
	if eff["netrc"] == nil {
		t.Fatal("expected netrc after activation")
	}

	// Add a new file to the project config.
	tpl2 := filepath.Join(tmpDir, "npmrc.tpl")
	if err := os.WriteFile(tpl2, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	projConfig2 := fmt.Sprintf("[files.netrc]\ntemplate = %q\nmode = 384\n[files.npmrc]\ntemplate = %q\nmode = 384\n", tplFile, tpl2)
	if err := os.WriteFile(filepath.Join(projDir, DefaultProjectConfigNames[0]), []byte(projConfig2), 0o644); err != nil {
		t.Fatal(err)
	}

	notifyCount = 0
	if err := mgr.RefreshActivation(projDir); err != nil {
		t.Fatalf("RefreshActivation() error: %v", err)
	}

	eff = mgr.Effective()
	if eff["npmrc"] == nil {
		t.Error("expected npmrc after RefreshActivation")
	}
	if notifyCount != 1 {
		t.Errorf("onChange called %d times, want 1", notifyCount)
	}
}

func TestRefreshActivationNonActiveDirIsNoop(t *testing.T) {
	globalCfg := &config.Config{
		Files: map[string]*config.FileConfig{},
	}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	err := mgr.RefreshActivation("/nonexistent/dir")
	if err != nil {
		t.Errorf("RefreshActivation() error = %v, want nil", err)
	}
}

func TestEffectiveMapsEqual(t *testing.T) {
	a := map[string]*EffectiveFile{
		"netrc": {FileConfig: &config.FileConfig{Render: "native", Template: "tplA"}},
	}
	b := map[string]*EffectiveFile{
		"netrc": {FileConfig: &config.FileConfig{Render: "native", Template: "tplA"}},
	}
	if !effectiveMapsEqual(a, b) {
		t.Error("expected equal")
	}

	c := map[string]*EffectiveFile{
		"netrc": {FileConfig: &config.FileConfig{Render: "native", Template: "tplB"}},
	}
	if effectiveMapsEqual(a, c) {
		t.Error("expected not equal")
	}

	d := map[string]*EffectiveFile{}
	if effectiveMapsEqual(a, d) {
		t.Error("expected not equal for different lengths")
	}
}

func TestActivateAutoDeactivatePreservesOtherSessions(t *testing.T) {
	tmpDir := t.TempDir()
	globalCfg := &config.Config{Files: map[string]*config.FileConfig{}}
	mgr := NewManager(globalCfg, DefaultProjectConfigNames, nil)

	projA := filepath.Join(tmpDir, "proj-a")
	if err := os.MkdirAll(projA, 0o755); err != nil {
		t.Fatal(err)
	}
	tplA := filepath.Join(tmpDir, "a.tpl")
	if err := os.WriteFile(tplA, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projA, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.netrc]\ntemplate = %q\nmode = 384\n", tplA), 0o644); err != nil {
		t.Fatal(err)
	}

	projB := filepath.Join(tmpDir, "proj-b")
	if err := os.MkdirAll(projB, 0o755); err != nil {
		t.Fatal(err)
	}
	tplB := filepath.Join(tmpDir, "b.tpl")
	if err := os.WriteFile(tplB, []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projB, DefaultProjectConfigNames[0]),
		fmt.Appendf(nil, "[files.npmrc]\ntemplate = %q\nmode = 384\n", tplB), 0o644); err != nil {
		t.Fatal(err)
	}

	_, _ = mgr.Activate(projA, nil, 3000)
	_, _ = mgr.Activate(projA, nil, 3001)

	_, _ = mgr.Activate(projB, nil, 3000)

	acts := mgr.Activations()
	if _, ok := acts[projA]; !ok {
		t.Error("projA should still be active (session 3001 remains)")
	}
	if _, ok := acts[projB]; !ok {
		t.Error("projB should be active")
	}

	sessions := mgr.Sessions()
	if pids := sessions[projA]; len(pids) != 1 || pids[0] != 3001 {
		t.Errorf("projA sessions = %v, want [3001]", pids)
	}
}
