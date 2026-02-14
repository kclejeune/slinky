package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/cipher"
	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/resolver"
)

func renderCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "render <name>",
		Short:   "Render a single file to stdout (for debugging)",
		GroupID: "debug",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgFile)
			if err != nil {
				return fmt.Errorf("loading config: %w", err)
			}

			ageCipher, err := cipher.New(string(cfg.Settings.Cache.Cipher))
			if err != nil {
				return fmt.Errorf("initializing cipher: %w", err)
			}

			secretCache := cache.New(ageCipher)
			defer secretCache.Stop()

			secretResolver := resolver.New(cfg, secretCache)
			content, err := secretResolver.RenderOnly(args[0])
			if err != nil {
				return err
			}

			_, err = os.Stdout.Write(content)
			return err
		},
	}
}

func cacheCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "cache",
		Short:   "Cache management commands",
		GroupID: "debug",
	}

	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Clear all cached entries",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Cache clear requires a running daemon. Use SIGHUP to trigger a cache clear.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:   "stats",
		Short: "Show cache statistics",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println("Cache stats requires a running daemon.")
			return nil
		},
	})

	return cmd
}
