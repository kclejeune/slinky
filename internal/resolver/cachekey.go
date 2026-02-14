package resolver

import (
	"crypto/sha256"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/kclejeune/slinky/internal/config"
)

type CacheKey struct {
	Hash     [32]byte
	FilePath string
}

func (k CacheKey) String() string {
	return fmt.Sprintf("%x:%s", k.Hash, k.FilePath)
}

// ComputeCacheKey derives a cache key from SHA-256 of the template file
// contents (or command+args for command mode) combined with the logical name.
func ComputeCacheKey(cfg *config.FileConfig) (CacheKey, error) {
	h := sha256.New()

	if cfg.Template != "" {
		tplPath := config.ExpandPath(cfg.Template)
		content, err := os.ReadFile(tplPath)
		if err != nil {
			return CacheKey{}, fmt.Errorf("reading template for cache key: %w", err)
		}
		h.Write(content)
	} else {
		parts := slices.Concat([]string{cfg.Command}, cfg.Args)
		h.Write([]byte(strings.Join(parts, "\x00")))
	}

	var hash [32]byte
	copy(hash[:], h.Sum(nil))

	return CacheKey{
		Hash:     hash,
		FilePath: cfg.Name,
	}, nil
}
