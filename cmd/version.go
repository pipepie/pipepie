package cmd

import (
	"fmt"
	"runtime"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

// Version is set by goreleaser at build time.
var Version = "dev"

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version",
	Run: func(cmd *cobra.Command, args []string) {
		name := lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true).Render("pie")
		ver := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true).Render(Version)
		info := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4")).Render(
			fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		)
		fmt.Printf("%s %s %s\n", name, ver, info)
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
