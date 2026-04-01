package cmd

import (
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Add a server connection",
	Long: `Adds a server to your accounts and sets it as active.

  pie login
  pie login --server host:9443 --key a7f3bc21...`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()

		server, _ := cmd.Flags().GetString("server")
		key, _ := cmd.Flags().GetString("key")
		name, _ := cmd.Flags().GetString("name")

		// Interactive if flags not provided
		if server == "" || key == "" {
			err := huh.NewForm(
				huh.NewGroup(
					huh.NewInput().
						Title("Server address").
						Description("From 'pie setup' output").
						Placeholder("tunnel.mysite.com:9443").
						Value(&server),
					huh.NewInput().
						Title("Server public key").
						Description("64 hex chars from 'pie setup'").
						Value(&key),
					huh.NewInput().
						Title("Default subdomain").
						Description("Leave empty for auto-generated").
						Placeholder("my-app").
						Value(&name),
				),
			).WithTheme(huh.ThemeDracula()).Run()
			if err != nil {
				return err
			}
		}

		if server == "" {
			return fmt.Errorf("server address required")
		}
		keyBytes, err := hex.DecodeString(key)
		if err != nil || len(keyBytes) != 32 {
			return fmt.Errorf("invalid key — must be 64 hex characters")
		}

		// Test connection
		var connOK bool
		spinner.New().
			Title("Testing connection...").
			Action(func() {
				conn, err := net.DialTimeout("tcp", server, 3*time.Second)
				if err == nil {
					conn.Close()
					connOK = true
				}
			}).Run()

		// Derive account name from server host
		accountName := server
		if host, _, err := net.SplitHostPort(server); err == nil {
			accountName = host
		}
		// Strip common prefixes
		accountName = strings.TrimPrefix(accountName, "tunnel.")

		acc := &config.Account{
			Type:      "self-hosted",
			Server:    server,
			Key:       key,
			Subdomain: name,
		}
		cfg.AddAccount(accountName, acc)

		if err := config.SaveClient(cfg); err != nil {
			return fmt.Errorf("save: %w", err)
		}

		// Summary
		okStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
		cyanS := lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
		pinkS := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true)
		dimS := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
		boldS := lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)

		status := okStyle.Render("connected")
		if !connOK {
			status = dimS.Render("unreachable (saved anyway)")
		}

		box := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#50fa7b")).
			Padding(1, 2)

		fmt.Println()
		fmt.Println(box.Render(
			okStyle.Render("✓ Logged in!")+"\n\n"+
				dimS.Render("Account   ")+boldS.Render(accountName)+dimS.Render(" (self-hosted)")+"\n"+
				dimS.Render("Server    ")+cyanS.Render(server)+"\n"+
				dimS.Render("Key       ")+pinkS.Render(key[:16]+"...")+"\n"+
				dimS.Render("Status    ")+status+"\n\n"+
				boldS.Render("Now just run:")+"\n"+
				cyanS.Render("  pie connect 3000"),
		))
		fmt.Println()

		return nil
	},
}

func init() {
	loginCmd.Flags().String("server", "", "Server tunnel address")
	loginCmd.Flags().String("key", "", "Server public key (hex)")
	loginCmd.Flags().StringP("name", "n", "", "Default subdomain")
	rootCmd.AddCommand(loginCmd)
}
