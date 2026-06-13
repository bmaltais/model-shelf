package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bmaltais/model-shelf/internal/config"
	"github.com/bmaltais/model-shelf/internal/detect"
	"github.com/bmaltais/model-shelf/internal/resolver"
	"github.com/bmaltais/model-shelf/internal/tui"
)

func initCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "init [path]",
		Short: "Create the shelf directory (optionally at a new path)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var chosenPath string
			explicit := len(args) == 1

			if explicit {
				chosenPath = args[0]
			} else {
				var err error
				chosenPath, err = pickShelfPath()
				if err != nil {
					return err
				}
				if chosenPath == "" {
					fmt.Fprintln(os.Stderr, "model-shelf: cancelled")
					os.Exit(1)
				}
			}

			cfg := &resolver.Config{ShelfRoot: chosenPath, AllowDownloads: true}
			created, err := resolver.InitShelf(cfg)
			if err != nil {
				return fmt.Errorf("init shelf: %w", err)
			}

			if explicit {
				configPath := config.WritableConfigPath(cfgPath)
				if err := config.WriteConfig(configPath, chosenPath, true); err != nil {
					return fmt.Errorf("writing config: %w", err)
				}
				fmt.Printf("model-shelf: wrote %s\n", configPath)
				fmt.Printf("            shelf_root = %s  (pinned)\n", chosenPath)
			} else {
				fmt.Printf("model-shelf: using %s  (auto-discovered, not pinned in config)\n", chosenPath)
			}

			if len(created) == 0 {
				fmt.Printf("model-shelf: shelf at %s already initialized\n", chosenPath)
				return nil
			}
			fmt.Printf("model-shelf: initialized shelf at %s\n", chosenPath)
			for _, p := range created {
				fmt.Printf("  + %s\n", p)
			}
			return nil
		},
	}
	return cmd
}

func pickShelfPath() (string, error) {
	candidates := detect.Candidates()

	// Exactly one existing external shelf — use silently.
	var existingExternal []detect.Candidate
	for _, c := range candidates {
		if c.Existing && c.IsExternal {
			existingExternal = append(existingExternal, c)
		}
	}
	if len(existingExternal) == 1 {
		fmt.Printf("model-shelf: using existing shelf at %s\n", existingExternal[0].Path)
		return existingExternal[0].Path, nil
	}

	// Non-TTY: require explicit path.
	if !isTTY() {
		if len(existingExternal) > 1 {
			fmt.Fprintln(os.Stderr, "error: multiple existing external shelves; specify a path explicitly:")
			for _, c := range existingExternal {
				fmt.Fprintf(os.Stderr, "  - %s\n", c.Path)
			}
		} else {
			fmt.Fprintln(os.Stderr, "model-shelf: not interactive; specify a path explicitly with `model-shelf init <path>`")
		}
		return "", nil
	}

	return tui.PickShelfPath(candidates)
}

func isTTY() bool {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (stat.Mode() & os.ModeCharDevice) != 0
}
