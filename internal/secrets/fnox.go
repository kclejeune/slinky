package secrets

import "context"

// FnoxGet resolves a secret by key via the fnox CLI (`fnox get KEY`).
// fnox discovers fnox.toml by walking up from the working directory, so
// project-scoped files resolve against their own project's secrets.
func FnoxGet(ctx context.Context, key string, opts Options) (string, error) {
	s := Settings().Fnox

	bin := s.Bin
	if bin == "" {
		bin = "fnox"
	}

	args := []string{"get", key}
	if s.Profile != "" {
		args = append(args, "--profile", s.Profile)
	}

	return runCLI(ctx, bin, args, opts, nil)
}
