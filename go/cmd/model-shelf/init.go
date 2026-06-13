package main

import "github.com/spf13/cobra"

func initCmd() *cobra.Command {
	return &cobra.Command{Use: "init", Short: "Create the shelf directory", Hidden: true}
}
