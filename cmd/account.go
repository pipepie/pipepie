package cmd

import (
	"fmt"
	"sort"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	accPurple = lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true)
	accGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	accCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	accDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
	accBold   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)
	accYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f1fa8c"))
	accBox    = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#bd93f9")).
			Padding(0, 2)
)

var accountCmd = &cobra.Command{
	Use:   "account",
	Short: "Manage server accounts",
	Long:  "Show, switch, or remove server connections.",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadClient()
		if err != nil {
			return err
		}

		if len(cfg.Accounts) == 0 {
			fmt.Println()
			fmt.Println(accDim.Render("  No accounts. Run ") + accCyan.Render("pie login") + accDim.Render(" to add one."))
			fmt.Println()
			return nil
		}

		// Sort names
		names := make([]string, 0, len(cfg.Accounts))
		for k := range cfg.Accounts {
			names = append(names, k)
		}
		sort.Strings(names)

		fmt.Println()
		fmt.Println(accBox.Render(accPurple.Render("Accounts")))
		fmt.Println()

		for _, name := range names {
			acc := cfg.Accounts[name]
			marker := "  "
			if name == cfg.Active {
				marker = accGreen.Render("● ")
			}

			typeLabel := accDim.Render("self-hosted")
			if acc.Type == "managed" {
				plan := acc.Plan
				if plan == "" {
					plan = "free"
				}
				typeLabel = accYellow.Render(fmt.Sprintf("managed (plan: %s)", plan))
			}

			fmt.Printf("  %s%-24s %s\n", marker, accBold.Render(name), typeLabel)
			fmt.Printf("    %s %s\n", accDim.Render("server"), accCyan.Render(acc.Server))
			if acc.Subdomain != "" {
				fmt.Printf("    %s %s\n", accDim.Render("subdomain"), acc.Subdomain)
			}
			fmt.Println()
		}

		if cfg.Active != "" {
			fmt.Println(accDim.Render("  Active: ") + accGreen.Render(cfg.Active))
		}
		fmt.Println()
		fmt.Println(accDim.Render("  Switch:  ") + accCyan.Render("pie account use <name>"))
		fmt.Println(accDim.Render("  Add:     ") + accCyan.Render("pie login"))
		fmt.Println(accDim.Render("  Remove:  ") + accCyan.Render("pie logout <name>"))
		fmt.Println()

		return nil
	},
}

var accountUseCmd = &cobra.Command{
	Use:   "use <name>",
	Short: "Switch active account",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadClient()
		if err != nil {
			return err
		}
		name := args[0]
		if err := cfg.SetActive(name); err != nil {
			return err
		}
		if err := config.SaveClient(cfg); err != nil {
			return err
		}

		acc := cfg.Accounts[name]
		typeLabel := "self-hosted"
		if acc.Type == "managed" {
			typeLabel = "managed"
		}

		fmt.Println()
		fmt.Println(accGreen.Render("  ✓") + " Switched to " + accBold.Render(name) + accDim.Render(" ("+typeLabel+")"))
		fmt.Println()

		return nil
	},
}

func init() {
	accountCmd.AddCommand(accountUseCmd)
	rootCmd.AddCommand(accountCmd)
}
