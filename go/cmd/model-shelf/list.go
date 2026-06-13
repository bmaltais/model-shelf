package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/bmaltais/model-shelf/internal/resolver"
)

func listCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List models on the curated shelf",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if err := resolver.CheckStorageAvailable(cfg); err != nil {
				return err
			}
			candidates := resolver.ListCandidates(cfg)
			for i, root := range candidates {
				tag := "primary"
				if i > 0 {
					tag = "additional"
				}
				if i > 0 {
					fmt.Println()
				}
				fmt.Printf("shelf  %s  (%s)\n", root, tag)
				printShelfContents(root)
			}
			return nil
		},
	}
}

func printShelfContents(root string) {
	for _, format := range resolver.SupportedFormats {
		sub := filepath.Join(root, format)
		fmt.Printf("\n  %s/\n", format)
		publishers, err := readDirs(sub)
		if err != nil || len(publishers) == 0 {
			fmt.Println("    (empty)")
			continue
		}
		any := false
		for _, publisher := range publishers {
			repos, _ := readDirs(filepath.Join(sub, publisher))
			for _, repo := range repos {
				repoPath := filepath.Join(sub, publisher, repo)
				if format == "gguf" {
					files, _ := filepath.Glob(filepath.Join(repoPath, "*.gguf"))
					for _, f := range files {
						if filepath.Base(f)[0] == '.' {
							continue
						}
						info, _ := os.Stat(f)
						size := int64(0)
						if info != nil {
							size = info.Size()
						}
						fmt.Printf("    %s/%s/%s  (%s)\n", publisher, repo, filepath.Base(f), fmtSize(size))
						any = true
					}
				} else {
					fmt.Printf("    %s/%s/\n", publisher, repo)
					any = true
				}
			}
		}
		if !any {
			fmt.Println("    (empty)")
		}
	}
}

func readDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() && e.Name()[0] != '.' {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func fmtSize(n int64) string {
	f := float64(n)
	for _, unit := range []string{"B", "KB", "MB", "GB", "TB"} {
		if f < 1024 || unit == "TB" {
			return fmt.Sprintf("%.1f %s", f, unit)
		}
		f /= 1024
	}
	return fmt.Sprintf("%.1f TB", f)
}
