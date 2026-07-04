package secrets

import "context"

// SecretSpecGet resolves a secret by key via the secretspec CLI
// (`secretspec get KEY`). secretspec discovers secretspec.toml from the
// working directory, so project-scoped files resolve against their own
// project's declarations.
func SecretSpecGet(ctx context.Context, key string, opts Options) (string, error) {
	s := Settings().SecretSpec

	bin := s.Bin
	if bin == "" {
		bin = "secretspec"
	}

	args := []string{"get", key}
	if s.Profile != "" {
		args = append(args, "--profile", s.Profile)
	}
	if s.Provider != "" {
		args = append(args, "--provider", s.Provider)
	}

	return runCLI(ctx, bin, args, opts, nil)
}
