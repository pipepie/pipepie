package cmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/Seinarukiro2/pipepie/internal/client"
	"github.com/Seinarukiro2/pipepie/internal/config"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [port]",
	Short: "Connect to server and forward traffic to localhost",
	Long: `Establishes an encrypted tunnel to the pipepie server.
Uses saved config from 'pie login' — or override with flags.

  # After 'pie login':
  pie connect 3000
  pie connect 5173 --name my-app

  # Without login (all flags):
  pie connect --server host:9443 --key abc... 3000

  # TCP tunnel:
  pie connect --tcp 5432`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()

		// Resolve from flags > active account > defaults
		savedServer, savedKey, savedSub := "", "", ""
		if active != nil {
			savedServer = active.Server
			savedKey = active.Key
			savedSub = active.Subdomain
		}

		server := resolveFlag(cmd, "server", savedServer, "localhost:9443")
		keyHex := resolveFlag(cmd, "key", savedKey, "")
		subdomain := resolveFlag(cmd, "name", savedSub, "")
		forward := resolveFlag(cmd, "forward", "", "http://localhost:3000")
		tcpForward := mustStr(cmd, "tcp")

		// Validate key
		if keyHex == "" {
			client.NotLoggedIn()
			return fmt.Errorf("no server configured")
		}
		keyBytes, err := hex.DecodeString(keyHex)
		if err != nil || len(keyBytes) != 32 {
			client.NotLoggedIn()
			return fmt.Errorf("invalid key in config")
		}

		// Shorthand: pie connect 3000
		if len(args) == 1 && !cmd.Flags().Changed("forward") && tcpForward == "" {
			forward = "http://localhost:" + args[0]
		}

		// TCP mode: pie connect --tcp 5432
		if tcpForward != "" {
			if _, err := fmt.Sscanf(tcpForward, "%d", new(int)); err == nil {
				tcpForward = "localhost:" + tcpForward
			}
		}

		// Resolve subdomain: --name flag > port cache > interactive
		port := extractPort(forward)
		if subdomain == "" && active != nil && port != "" {
			subdomain = active.GetTunnelName(port)
		}
		if subdomain == "" && tcpForward == "" {
			var subChoice string
			huh.NewForm(
				huh.NewGroup(
					huh.NewSelect[string]().
						Title("Subdomain").
						Options(
							huh.NewOption("Choose my own (stable URL)", "custom"),
							huh.NewOption("Random (auto-generated)", "random"),
						).
						Value(&subChoice),
				),
			).WithTheme(huh.ThemeDracula()).Run()

			if subChoice == "custom" {
				huh.NewForm(
					huh.NewGroup(
						huh.NewInput().
							Title("Subdomain name").
							Placeholder("my-app").
							Value(&subdomain),
					),
				).WithTheme(huh.ThemeDracula()).Run()
				subdomain = strings.TrimSpace(subdomain)
			}
		}

		// If --name was explicitly set, override cache
		if cmd.Flags().Changed("name") && active != nil && port != "" {
			active.SetTunnelName(port, subdomain)
			config.SaveClient(cfg)
		}

		auth := mustStr(cmd, "auth")

		clientCfg := client.Config{
			ServerAddr:   server,
			ServerPubKey: keyBytes,
			Subdomain:    subdomain,
			Forward:      forward,
			TCPForward:   tcpForward,
			Auth:         auth,
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		c := client.New(clientCfg)
		err = c.Run(ctx)

		// Save assigned subdomain to cache for next time
		if active != nil && port != "" && c.AssignedSubdomain() != "" {
			active.SetTunnelName(port, c.AssignedSubdomain())
			config.SaveClient(cfg)
		}

		// Clean exit message
		if ctx.Err() != nil {
			fmt.Println()
			fmt.Println(lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4")).Render("  Tunnel closed."))
			fmt.Println()
			return nil
		}
		return err
	},
	Args: cobra.MaximumNArgs(1),
}

func extractPort(forward string) string {
	// "http://localhost:3000" → "3000"
	if i := strings.LastIndex(forward, ":"); i != -1 {
		p := forward[i+1:]
		// Strip path if any
		if j := strings.Index(p, "/"); j != -1 {
			p = p[:j]
		}
		return p
	}
	return ""
}

// resolveFlag returns: explicit flag > saved config > default
func resolveFlag(cmd *cobra.Command, name, saved, fallback string) string {
	if cmd.Flags().Changed(name) {
		v, _ := cmd.Flags().GetString(name)
		return v
	}
	if saved != "" {
		return saved
	}
	return fallback
}

func mustStr(cmd *cobra.Command, name string) string {
	v, _ := cmd.Flags().GetString(name)
	return v
}

func init() {
	connectCmd.Flags().String("server", "", "Server address (from 'pie login')")
	connectCmd.Flags().String("key", "", "Server public key (from 'pie login')")
	connectCmd.Flags().StringP("name", "n", "", "Subdomain name (empty = auto)")
	connectCmd.Flags().String("forward", "", "Local HTTP target")
	connectCmd.Flags().String("tcp", "", "Local TCP target (e.g. 5432)")
	connectCmd.Flags().String("auth", "", "Password to protect public URL")

	rootCmd.AddCommand(connectCmd)
}
