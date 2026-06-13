package main

import "github.com/spf13/cobra"

func resolveCmd() *cobra.Command {
	return &cobra.Command{Use: "resolve", Short: "Resolve a model to a local path", Hidden: true}
}
