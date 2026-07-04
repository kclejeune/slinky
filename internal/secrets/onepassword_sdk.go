//go:build cgo || windows

package secrets

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	onepassword "github.com/1password/onepassword-sdk-go"
)

// sdkSupported reports whether the 1Password Go SDK is compiled into this
// binary. The SDK's desktop-app integration requires CGO on macOS and
// Linux, and the SDK package itself does not compile in pure-Go builds on
// those platforms, so all SDK use is gated behind this build tag. Pure-Go
// builds fall back to the op CLI.
const sdkSupported = true

// sdkClients caches authenticated SDK clients per auth plan. Client
// construction is expensive (WASM core initialization plus an auth
// handshake — for desktop-app auth, potentially a human authorization
// prompt), and the SDK re-authenticates automatically when the app is
// locked and unlocked, so clients are long-lived.
var (
	sdkMu      sync.Mutex
	sdkClients = map[string]*onepassword.Client{}
)

func sdkClientKey(plan opAuthPlan) string {
	return plan.method + "\x00" + plan.token + "\x00" + plan.account
}

// resetSDKClients drops cached clients. Called from Configure so auth
// changes (new token, different account) take effect on config reload.
func resetSDKClients() {
	sdkMu.Lock()
	defer sdkMu.Unlock()
	sdkClients = map[string]*onepassword.Client{}
}

func sdkClient(ctx context.Context, plan opAuthPlan) (*onepassword.Client, error) {
	sdkMu.Lock()
	defer sdkMu.Unlock()

	key := sdkClientKey(plan)
	if c, ok := sdkClients[key]; ok {
		return c, nil
	}

	opts := []onepassword.ClientOption{
		onepassword.WithIntegrationInfo("slinky", integrationVersion()),
	}
	switch plan.method {
	case "sdk-service-account":
		opts = append(opts, onepassword.WithServiceAccountToken(plan.token))
	case "sdk-desktop-app":
		slog.Info(
			"1Password: authenticating via desktop app (approve the prompt in the 1Password app if asked)",
			"account",
			plan.account,
		)
		opts = append(opts, onepassword.WithDesktopAppIntegration(plan.account))
	default:
		return nil, fmt.Errorf("unknown SDK auth method %q", plan.method)
	}

	client, err := onepassword.NewClient(ctx, opts...)
	if err != nil {
		return nil, err
	}

	sdkClients[key] = client
	return client, nil
}

// sdkResolve resolves an op:// secret reference with the official SDK.
func sdkResolve(ctx context.Context, plan opAuthPlan, ref string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	client, err := sdkClient(ctx, plan)
	if err != nil {
		return "", fmt.Errorf("creating client: %w", err)
	}

	return client.Secrets().Resolve(ctx, ref)
}
