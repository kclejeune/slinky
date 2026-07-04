package secrets

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

// writeStub creates an executable shell script named bin in dir that
// echoes its arguments and selected env/cwd details, then returns dir for
// PATH prepending.
func writeStub(t *testing.T, dir, bin, script string) {
	t.Helper()
	path := filepath.Join(dir, bin)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func stubEnv(t *testing.T, binDir string) Options {
	t.Helper()
	return Options{Env: map[string]string{
		"PATH": binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}}
}

func configure(t *testing.T, s config.IntegrationsSettings) {
	t.Helper()
	Configure(s, "test")
	t.Cleanup(func() { Configure(config.IntegrationsSettings{}, "test") })
}

func TestFnoxGet(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "fnox", `[ "$1" = "get" ] || exit 2
echo "args:$@"`)
	configure(t, config.IntegrationsSettings{})

	out, err := FnoxGet(context.Background(), "DATABASE_URL", stubEnv(t, dir))
	if err != nil {
		t.Fatalf("FnoxGet() error: %v", err)
	}
	if out != "args:get DATABASE_URL" {
		t.Errorf("FnoxGet() = %q", out)
	}
}

func TestFnoxGetProfile(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "fnox", `echo "args:$@"`)
	configure(t, config.IntegrationsSettings{
		Fnox: config.FnoxSettings{Profile: "production"},
	})

	out, err := FnoxGet(context.Background(), "KEY", stubEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if out != "args:get KEY --profile production" {
		t.Errorf("FnoxGet() = %q", out)
	}
}

func TestFnoxGetWorkDir(t *testing.T) {
	dir := t.TempDir()
	workDir := t.TempDir()
	writeStub(t, dir, "fnox", `pwd`)
	configure(t, config.IntegrationsSettings{})

	opts := stubEnv(t, dir)
	opts.WorkDir = workDir

	out, err := FnoxGet(context.Background(), "KEY", opts)
	if err != nil {
		t.Fatal(err)
	}
	// Resolve symlinks (macOS /tmp → /private/tmp).
	want, _ := filepath.EvalSymlinks(workDir)
	got, _ := filepath.EvalSymlinks(out)
	if got != want {
		t.Errorf("provider cwd = %q, want %q", out, workDir)
	}
}

func TestFnoxGetMissingBinary(t *testing.T) {
	configure(t, config.IntegrationsSettings{
		Fnox: config.FnoxSettings{Bin: "definitely-not-a-real-binary-xyz"},
	})

	_, err := FnoxGet(context.Background(), "KEY", Options{})
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "is it installed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFnoxGetCommandFailure(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "fnox", `echo "no such secret" >&2; exit 1`)
	configure(t, config.IntegrationsSettings{})

	_, err := FnoxGet(context.Background(), "MISSING", stubEnv(t, dir))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no such secret") {
		t.Errorf("error should include stderr, got: %v", err)
	}
}

func TestSecretSpecGet(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "secretspec", `echo "args:$@"`)
	configure(t, config.IntegrationsSettings{
		SecretSpec: config.SecretSpecSettings{Profile: "development", Provider: "keyring"},
	})

	out, err := SecretSpecGet(context.Background(), "DATABASE_URL", stubEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if out != "args:get DATABASE_URL --profile development --provider keyring" {
		t.Errorf("SecretSpecGet() = %q", out)
	}
}

func TestSecretSpecGetDefaults(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "secretspec", `echo "args:$@"`)
	configure(t, config.IntegrationsSettings{})

	out, err := SecretSpecGet(context.Background(), "KEY", stubEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if out != "args:get KEY" {
		t.Errorf("SecretSpecGet() = %q", out)
	}
}

func TestRunCLITrimsTrailingNewlines(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "fnox", `printf 'value\n\n'`)
	configure(t, config.IntegrationsSettings{})

	out, err := FnoxGet(context.Background(), "KEY", stubEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if out != "value" {
		t.Errorf("got %q, want %q", out, "value")
	}
}

func TestRunCLIActivationEnvPropagates(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "fnox", `echo "$MY_ACTIVATION_VAR"`)
	configure(t, config.IntegrationsSettings{})

	opts := stubEnv(t, dir)
	opts.Env["MY_ACTIVATION_VAR"] = "from-activation"

	out, err := FnoxGet(context.Background(), "KEY", opts)
	if err != nil {
		t.Fatal(err)
	}
	if out != "from-activation" {
		t.Errorf("got %q, want %q", out, "from-activation")
	}
}
