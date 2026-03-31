// Package cmd implements the pipepie CLI.
package cmd

import (
	"os"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "pie",
	Short: "Self-hosted webhook relay with inspection",
	Long: lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true).Render("pie") + ` — receive webhooks on a public URL and relay them to localhost.

  Getting started:
    pie login              Add a server connection
    pie connect 3000       Forward localhost:3000 to a public URL
    pie dashboard          Open web dashboard in browser

  Server (self-hosted):
    pie setup              Interactive server setup wizard
    pie server             Start the relay server
    pie doctor             Check server configuration

  Management:
    pie account            Show & switch server accounts
    pie status             Show tunnels and activity
    pie logs <tunnel>      Stream recent requests
    pie up                 Multi-tunnel from pipepie.yaml
    pie logout             Remove a server account`,
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
