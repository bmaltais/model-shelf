package main

import "github.com/spf13/cobra"

func findCmd() *cobra.Command {
	return &cobra.Command{Use: "find", Short: "Search Hugging Face Hub", Hidden: true}
}
