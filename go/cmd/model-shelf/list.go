package main

import "github.com/spf13/cobra"

func listCmd() *cobra.Command {
	return &cobra.Command{Use: "list", Short: "List models on the shelf", Hidden: true}
}
