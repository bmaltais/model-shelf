// Command model-shelf is a local-first resolver for Hugging Face models.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

var cfgPath string

func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "model-shelf",
		Short: "Local-first resolver for Hugging Face models (gguf, mlx, safetensors).",
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
	}
	root.PersistentFlags().StringVar(&cfgPath, "config", "",
		"path to config.toml (default: $MODEL_SHELF_CONFIG or ~/.config/model-shelf/config.toml)")

	root.AddCommand(versionCmd())
	root.AddCommand(resolveCmd())
	root.AddCommand(initCmd())
	root.AddCommand(findCmd())
	root.AddCommand(listCmd())
	return root
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("model-shelf %s (go)\n", version)
		},
	}
}

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
