package cmd

import (
	"github.com/pipepie/pipepie/internal/doctor"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Run diagnostic checks on server configuration",
	Long: `Verifies your pipepie server setup:
  - Config file validity
  - Port availability
  - DNS resolution (base + wildcard)
  - TLS certificate status and expiry
  - SQLite database access
  - Disk space
  - Network connectivity
  - System limits (file descriptors)`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		return doctor.Run(cfgPath)
	},
}

func init() {
	doctorCmd.Flags().String("config", "pipepie.yaml", "Config file to check")
	rootCmd.AddCommand(doctorCmd)
}
