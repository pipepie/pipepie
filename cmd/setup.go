package cmd

import (
	"github.com/pipepie/pipepie/internal/setup"
	"github.com/spf13/cobra"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive server setup wizard",
	Long: `Guides you through setting up a pipepie server:
  - Domain configuration
  - DNS record verification
  - TLS certificate (Cloudflare auto or manual)
  - Admin token generation
  - Config file creation`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		return setup.Run(cfgPath)
	},
}

func init() {
	setupCmd.Flags().String("config", "pipepie.yaml", "Config file to write")
	rootCmd.AddCommand(setupCmd)
}
