package secrets

import (
	"context"
	"strings"
	"testing"

	"github.com/kclejeune/slinky/internal/config"
)

func TestPlanOPAuthExplicitCLI(t *testing.T) {
	plan, err := planOPAuth(config.OnePasswordSettings{Auth: config.OPAuthCLI}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.method != "cli" {
		t.Errorf("method = %q, want cli", plan.method)
	}
}

func TestPlanOPAuthServiceAccountRequiresToken(t *testing.T) {
	_, err := planOPAuth(
		config.OnePasswordSettings{Auth: config.OPAuthServiceAccount},
		Options{},
	)
	if err == nil {
		t.Fatal("expected error without token")
	}
	if !strings.Contains(err.Error(), "OP_SERVICE_ACCOUNT_TOKEN") {
		t.Errorf("error should name the token env var: %v", err)
	}
}

func TestPlanOPAuthServiceAccountFromActivationEnv(t *testing.T) {
	plan, err := planOPAuth(
		config.OnePasswordSettings{Auth: config.OPAuthServiceAccount},
		Options{Env: map[string]string{"OP_SERVICE_ACCOUNT_TOKEN": "ops_test"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.token != "ops_test" {
		t.Errorf("token = %q, want ops_test", plan.token)
	}
	wantMethod := "cli"
	if sdkSupported {
		wantMethod = "sdk-service-account"
	}
	if plan.method != wantMethod {
		t.Errorf("method = %q, want %q", plan.method, wantMethod)
	}
}

func TestPlanOPAuthCustomTokenEnv(t *testing.T) {
	plan, err := planOPAuth(
		config.OnePasswordSettings{
			Auth:     config.OPAuthServiceAccount,
			TokenEnv: "MY_OP_TOKEN",
		},
		Options{Env: map[string]string{"MY_OP_TOKEN": "ops_custom"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.token != "ops_custom" {
		t.Errorf("token = %q, want ops_custom", plan.token)
	}
}

func TestPlanOPAuthDesktopApp(t *testing.T) {
	s := config.OnePasswordSettings{Auth: config.OPAuthDesktopApp, Account: "my.1password.com"}
	plan, err := planOPAuth(s, Options{})

	if sdkSupported {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if plan.method != "sdk-desktop-app" || plan.account != "my.1password.com" {
			t.Errorf("plan = %+v", plan)
		}
	} else {
		if err == nil {
			t.Fatal("expected error in pure-Go build")
		}
		if !strings.Contains(err.Error(), "CGO") {
			t.Errorf("error should mention CGO: %v", err)
		}
	}
}

func TestPlanOPAuthDesktopAppRequiresAccount(t *testing.T) {
	if !sdkSupported {
		t.Skip("SDK not compiled in")
	}
	_, err := planOPAuth(config.OnePasswordSettings{Auth: config.OPAuthDesktopApp}, Options{})
	if err == nil {
		t.Fatal("expected error without account")
	}
	if !strings.Contains(err.Error(), "account") {
		t.Errorf("error should mention account: %v", err)
	}
}

func TestPlanOPAuthAutoPrefersToken(t *testing.T) {
	plan, err := planOPAuth(
		config.OnePasswordSettings{Account: "acct"},
		Options{Env: map[string]string{"OP_SERVICE_ACCOUNT_TOKEN": "ops_x"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if plan.token != "ops_x" {
		t.Errorf("auto mode should pick up token, got %+v", plan)
	}
}

func TestPlanOPAuthAutoFallsBackToCLI(t *testing.T) {
	// No token, no account → CLI regardless of build mode.
	plan, err := planOPAuth(config.OnePasswordSettings{}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if plan.method != "cli" {
		t.Errorf("method = %q, want cli", plan.method)
	}
}

func TestOnePasswordResolveCLI(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "op", `[ "$1" = "read" ] || exit 2
echo "resolved:$2 account:${OP_SERVICE_ACCOUNT_TOKEN:-none}"`)
	configure(t, config.IntegrationsSettings{
		OnePassword: config.OnePasswordSettings{Auth: config.OPAuthCLI},
	})

	out, err := OnePasswordResolve(
		context.Background(),
		"op://Private/GitHub/token",
		stubEnv(t, dir),
	)
	if err != nil {
		t.Fatalf("OnePasswordResolve() error: %v", err)
	}
	if out != "resolved:op://Private/GitHub/token account:none" {
		t.Errorf("got %q", out)
	}
}

func TestOnePasswordResolveCLIWithToken(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "op", `echo "token:${OP_SERVICE_ACCOUNT_TOKEN}"`)
	configure(t, config.IntegrationsSettings{
		OnePassword: config.OnePasswordSettings{Auth: config.OPAuthCLI},
	})

	opts := stubEnv(t, dir)
	opts.Env["OP_SERVICE_ACCOUNT_TOKEN"] = "ops_secret"

	out, err := OnePasswordResolve(context.Background(), "op://v/i/f", opts)
	if err != nil {
		t.Fatal(err)
	}
	if out != "token:ops_secret" {
		t.Errorf("got %q", out)
	}
}

func TestOnePasswordResolveCLIAccountFlag(t *testing.T) {
	dir := t.TempDir()
	writeStub(t, dir, "op", `echo "args:$@"`)
	configure(t, config.IntegrationsSettings{
		OnePassword: config.OnePasswordSettings{Auth: config.OPAuthCLI, Account: "work"},
	})

	out, err := OnePasswordResolve(context.Background(), "op://v/i/f", stubEnv(t, dir))
	if err != nil {
		t.Fatal(err)
	}
	if out != "args:read op://v/i/f --account work" {
		t.Errorf("got %q", out)
	}
}
