//go:build !cgo && (darwin || linux)

package secrets

import (
	"context"
	"errors"
)

// sdkSupported is false in pure-Go builds: the 1Password Go SDK's
// desktop-app integration requires CGO on macOS and Linux, and the SDK
// package does not compile with CGO disabled on those platforms. All
// resolution goes through the op CLI instead (which supports both the
// desktop app session and service account tokens).
const sdkSupported = false

func resetSDKClients() {}

func sdkResolve(ctx context.Context, plan opAuthPlan, ref string) (string, error) {
	return "", errors.New("1Password SDK is not available in pure-Go builds")
}
