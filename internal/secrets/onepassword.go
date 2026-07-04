package secrets

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/kclejeune/slinky/internal/config"
)

const defaultTokenEnv = "OP_SERVICE_ACCOUNT_TOKEN" //nolint:gosec // env var name, not a credential

// opAuthPlan is the concrete authentication decision for one resolution.
type opAuthPlan struct {
	// method is one of "sdk-service-account", "sdk-desktop-app", "cli".
	method  string
	token   string
	account string
}

// planOPAuth turns the configured auth mode into a concrete plan, taking
// into account whether the SDK is compiled in (CGO builds) and whether a
// service account token is present in the environment.
func planOPAuth(s config.OnePasswordSettings, opts Options) (opAuthPlan, error) {
	tokenEnv := s.TokenEnv
	if tokenEnv == "" {
		tokenEnv = defaultTokenEnv
	}
	token, _ := envValue(tokenEnv, opts)

	mode := s.Auth
	if mode == "" {
		mode = config.OPAuthAuto
	}

	switch mode {
	case config.OPAuthServiceAccount:
		if token == "" {
			return opAuthPlan{}, fmt.Errorf(
				"1Password service account auth: %s is not set", tokenEnv,
			)
		}
		if sdkSupported {
			return opAuthPlan{method: "sdk-service-account", token: token}, nil
		}
		// Pure-Go build: the op CLI honors the same token.
		return opAuthPlan{method: "cli", token: token}, nil

	case config.OPAuthDesktopApp:
		if !sdkSupported {
			return opAuthPlan{}, fmt.Errorf(
				"1Password desktop-app auth requires a CGO-enabled slinky build " +
					"(the SDK's desktop integration is unavailable in pure-Go builds); " +
					"rebuild with CGO_ENABLED=1 or set settings.integrations.onepassword.auth = \"cli\" " +
					"to use the op CLI's desktop app session instead",
			)
		}
		if s.Account == "" {
			return opAuthPlan{}, fmt.Errorf(
				"1Password desktop-app auth requires settings.integrations.onepassword.account " +
					"(your account name as shown in the desktop app sidebar)",
			)
		}
		return opAuthPlan{method: "sdk-desktop-app", account: s.Account}, nil

	case config.OPAuthCLI:
		return opAuthPlan{method: "cli", token: token}, nil

	default: // auto
		if token != "" {
			if sdkSupported {
				return opAuthPlan{method: "sdk-service-account", token: token}, nil
			}
			return opAuthPlan{method: "cli", token: token}, nil
		}
		if sdkSupported && s.Account != "" {
			return opAuthPlan{method: "sdk-desktop-app", account: s.Account}, nil
		}
		return opAuthPlan{method: "cli"}, nil
	}
}

// DescribeOnePasswordAuth returns a human-readable description of how the
// 1Password integration would authenticate right now, or an error if the
// configured mode cannot work. Used by `slinky doctor`.
func DescribeOnePasswordAuth(opts Options) (string, error) {
	plan, err := planOPAuth(Settings().OnePassword, opts)
	if err != nil {
		return "", err
	}

	switch plan.method {
	case "sdk-service-account":
		return "SDK, service account token", nil
	case "sdk-desktop-app":
		return fmt.Sprintf("SDK, desktop app session (account %q)", plan.account), nil
	default:
		if plan.token != "" {
			return "op CLI, service account token", nil
		}
		return "op CLI (desktop app session or op signin)", nil
	}
}

// OnePasswordResolve resolves an op://vault/item/field secret reference.
//
// Depending on configuration and build mode it uses the official 1Password
// Go SDK in-process (service account token, or the desktop app's
// authorization session in CGO builds) or shells out to the op CLI, which
// itself rides the desktop app session when the CLI integration is enabled.
func OnePasswordResolve(ctx context.Context, ref string, opts Options) (string, error) {
	s := Settings().OnePassword

	plan, err := planOPAuth(s, opts)
	if err != nil {
		return "", err
	}

	switch plan.method {
	case "sdk-service-account", "sdk-desktop-app":
		val, err := sdkResolve(ctx, plan, ref)
		if err != nil {
			return "", fmt.Errorf("1Password SDK (%s): %w", plan.method, err)
		}
		return val, nil

	default:
		return opCLIResolve(ctx, s, plan, ref, opts)
	}
}

// opCLIResolve reads a secret reference with `op read`. When a service
// account token is part of the plan it is passed through the environment;
// otherwise the CLI uses its own session (desktop app integration or
// `op signin`).
func opCLIResolve(
	ctx context.Context,
	s config.OnePasswordSettings,
	plan opAuthPlan,
	ref string,
	opts Options,
) (string, error) {
	bin := s.Bin
	if bin == "" {
		bin = "op"
	}

	var extraEnv map[string]string
	if plan.token != "" {
		extraEnv = map[string]string{defaultTokenEnv: plan.token}
	}

	slog.Debug("1Password: resolving via op CLI", "ref", ref)
	args := []string{"read", ref}
	// Select the account explicitly only when not using a service account
	// token (tokens are scoped to a single account already).
	if plan.token == "" && s.Account != "" {
		args = append(args, "--account", s.Account)
	}
	return runCLI(ctx, bin, args, opts, extraEnv)
}
