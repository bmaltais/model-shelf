package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/bmaltais/model-shelf/internal/search"
)

func findCmd() *cobra.Command {
	var (
		format  string
		limit   int
		jsonOut bool
	)

	cmd := &cobra.Command{
		Use:   "find <query>",
		Short: "Search Hugging Face Hub for models matching a query",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := search.FindModels(args[0], search.Options{
				Format: format,
				Limit:  limit,
			})
			if err != nil {
				return err
			}
			if jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				enc.Encode(results)
				if len(results) == 0 {
					os.Exit(1)
				}
				return nil
			}
			if len(results) == 0 {
				fmt.Println("(no results)")
				os.Exit(1)
			}
			for _, r := range results {
				fmt.Printf("  [%-11s] %-55s %10s downloads\n",
					r.Format, r.RepoID, formatDownloads(r.Downloads))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&format, "format", "", "filter to a single format (gguf, mlx, safetensors)")
	cmd.Flags().IntVar(&limit, "limit", 10, "max results")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit JSON")
	return cmd
}

func formatDownloads(n int) string {
	if n >= 1_000_000 {
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
	if n >= 1_000 {
		return fmt.Sprintf("%.1fK", float64(n)/1_000)
	}
	return fmt.Sprintf("%d", n)
}
