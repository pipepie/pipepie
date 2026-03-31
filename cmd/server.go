package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"os/signal"
	"syscall"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/server"
	"github.com/Seinarukiro2/pipepie/internal/setup"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the relay server",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := server.DefaultConfig()

		// Load from config file if provided (from pie setup)
		cfgFile, _ := cmd.Flags().GetString("config")
		if cfgFile != "" {
			if err := loadServerConfig(cfgFile, &cfg); err != nil {
				return err
			}
		}

		// CLI flags override config file
		if cmd.Flags().Changed("addr") {
			cfg.Addr, _ = cmd.Flags().GetString("addr")
		}
		if cmd.Flags().Changed("tunnel-addr") {
			cfg.TunnelAddr, _ = cmd.Flags().GetString("tunnel-addr")
		}
		if cmd.Flags().Changed("domain") {
			cfg.Domain, _ = cmd.Flags().GetString("domain")
		}
		if cmd.Flags().Changed("key-file") {
			cfg.KeyFile, _ = cmd.Flags().GetString("key-file")
		}
		if cmd.Flags().Changed("db") {
			cfg.DBPath, _ = cmd.Flags().GetString("db")
		}
		if cmd.Flags().Changed("auto-tls") {
			cfg.AutoTLS, _ = cmd.Flags().GetBool("auto-tls")
		}
		if cmd.Flags().Changed("tls-cert") {
			cfg.TLSCert, _ = cmd.Flags().GetString("tls-cert")
		}
		if cmd.Flags().Changed("tls-key") {
			cfg.TLSKey, _ = cmd.Flags().GetString("tls-key")
		}
		if cmd.Flags().Changed("retention") {
			retStr, _ := cmd.Flags().GetString("retention")
			if d, err := time.ParseDuration(retStr); err == nil {
				cfg.Retention = d
			}
		}

		banner()

		log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
		srv, err := server.New(cfg, log)
		if err != nil {
			return err
		}

		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			log.Info("shutting down...")
			srv.Close()
			os.Exit(0)
		}()

		return srv.Run()
	},
}

func loadServerConfig(path string, cfg *server.Config) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var sc setup.ServerConfig
	if err := yaml.Unmarshal(data, &sc); err != nil {
		return err
	}
	cfg.Domain = strings.TrimSpace(sc.Domain)
	cfg.TunnelAddr = sc.TunnelAddr
	cfg.DBPath = sc.DBPath
	cfg.KeyFile = sc.KeyFile
	if sc.HTTPAddr != "" {
		cfg.Addr = sc.HTTPAddr
	}
	switch sc.TLS.Mode {
	case "cloudflare":
		cfg.AutoTLS = true
		cfg.TLSCacheDir = sc.TLS.CacheDir
		cfg.CloudflareToken = sc.TLS.CFToken
	case "manual":
		cfg.TLSCert = sc.TLS.CertFile
		cfg.TLSKey = sc.TLS.KeyFile
	}
	return nil
}

func banner() {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#bd93f9")).
		Padding(0, 2)
	title := lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true).Render("pie")
	ver := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4")).Render("v" + Version)
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4")).Render("  tunnel server")

	fmt.Println()
	fmt.Println(box.Render(title + sub + "  " + ver))
	fmt.Println()
}

func init() {
	serverCmd.Flags().String("config", "", "Config file (from pie setup)")
	serverCmd.Flags().String("addr", ":8080", "HTTP listen address")
	serverCmd.Flags().String("tunnel-addr", ":9443", "Tunnel TCP listen address")
	serverCmd.Flags().String("domain", "localhost", "Base domain for subdomains")
	serverCmd.Flags().String("key-file", "pipepie.key", "Noise static key file")
	serverCmd.Flags().String("db", "pipepie.db", "SQLite database path")
	serverCmd.Flags().String("retention", "72h", "Request retention period")
	serverCmd.Flags().Bool("auto-tls", false, "Enable automatic TLS (Let's Encrypt)")
	serverCmd.Flags().String("tls-cache", "pipepie-certs", "TLS certificate cache directory")
	serverCmd.Flags().String("tls-cert", "", "TLS certificate file (PEM)")
	serverCmd.Flags().String("tls-key", "", "TLS private key file (PEM)")

	rootCmd.AddCommand(serverCmd)
}
