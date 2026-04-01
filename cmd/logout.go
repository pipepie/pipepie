package cmd

import (
	"fmt"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var logoutCmd = &cobra.Command{
	Use:   "logout [name]",
	Short: "Remove a server account",
	Long: `Removes an account from your config.
If no name given, shows interactive picker.

  pie logout tunnel.mysite.com
  pie logout`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadClient()
		if err != nil {
			return err
		}
		if len(cfg.Accounts) == 0 {
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4")).Render("  No accounts to remove."))
			return nil
		}

		name := ""
		if len(args) > 0 {
			name = args[0]
		} else {
			// Interactive picker
			options := make([]huh.Option[string], 0, len(cfg.Accounts))
			for k, acc := range cfg.Accounts {
				label := k
				if acc.Type == "managed" {
					label += " (managed)"
				} else {
					label += " (self-hosted)"
				}
				options = append(options, huh.NewOption(label, k))
			}
			huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Remove which account?").
						Options(options...).
						Value(&name),
				),
			).WithTheme(huh.ThemeDracula()).Run()
		}

		if name == "" {
			return nil
		}

		var confirm bool
		huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title(fmt.Sprintf("Remove %q?", name)).
					Value(&confirm),
			),
		).WithTheme(huh.ThemeDracula()).Run()

		if !confirm {
			return nil
		}

		if err := cfg.RemoveAccount(name); err != nil {
			return err
		}
		if err := config.SaveClient(cfg); err != nil {
			return err
		}

		green := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
		fmt.Println()
		fmt.Println(green.Render("  ✓") + " Removed " + name)
		if cfg.Active != "" {
			dim := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
			fmt.Println(dim.Render("  Active: ") + cfg.Active)
		}
		fmt.Println()

		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
