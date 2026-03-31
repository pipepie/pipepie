package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/config"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show tunnel status and recent activity",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		serverHTTP := resolveHTTPAddrFromAccount(cmd, active)

		bold := color.New(color.Bold)
		green := color.New(color.FgGreen, color.Bold)
		red := color.New(color.FgRed, color.Bold)
		cyan := color.New(color.FgCyan)
		dim := color.New(color.Faint)

		// Fetch overview
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(serverHTTP + "/api/overview")
		if err != nil {
			red.Println("  Cannot reach server")
			dim.Printf("  Tried: %s\n", serverHTTP)
			dim.Println("  Is the server running? Check with 'pie doctor'")
			return nil
		}
		defer resp.Body.Close()

		var data struct {
			Tunnels []struct {
				Subdomain    string `json:"subdomain"`
				Online       bool   `json:"online"`
				Protocol     string `json:"protocol"`
				RemoteAddr   string `json:"remote_addr"`
				Uptime       string `json:"uptime"`
				Total        int    `json:"total"`
				Success      int    `json:"success"`
				Errors       int    `json:"errors"`
				SuccessRate  string `json:"success_rate"`
				LastRequest  string `json:"last_request"`
				PublicURL    string `json:"public_url"`
			} `json:"tunnels"`
			TotalTunnels  int    `json:"total_tunnels"`
			Online        int    `json:"online"`
			TotalRequests int    `json:"total_requests"`
			Domain        string `json:"domain"`
		}
		body, _ := io.ReadAll(resp.Body)
		if err := json.Unmarshal(body, &data); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		// Header
		fmt.Println()
		bold.Printf("  pipepie server · %s\n", data.Domain)
		fmt.Println()

		// Summary
		dim.Print("  Tunnels  ")
		fmt.Printf("%d total, ", data.TotalTunnels)
		green.Printf("%d online\n", data.Online)
		dim.Print("  Requests ")
		fmt.Printf("%d total\n", data.TotalRequests)
		fmt.Println()

		if len(data.Tunnels) == 0 {
			dim.Println("  No tunnels registered.")
			return nil
		}

		// Table
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		bold.Fprintf(w, "  SUBDOMAIN\tSTATUS\tREQUESTS\tRATE\tLAST\tFROM\n")
		for _, t := range data.Tunnels {
			status := red.Sprint("offline")
			from := dim.Sprint("—")
			if t.Online {
				status = green.Sprint("online ")
				if t.RemoteAddr != "" {
					from = dim.Sprint(t.RemoteAddr)
				}
				if t.Uptime != "" {
					status = green.Sprintf("online  %s", dim.Sprintf("(%s)", t.Uptime))
				}
			}
			last := dim.Sprint("—")
			if t.LastRequest != "" {
				last = dim.Sprint(formatAgo(t.LastRequest))
			}
			errStr := ""
			if t.Errors > 0 {
				errStr = red.Sprintf(" (%d err)", t.Errors)
			}
			fmt.Fprintf(w, "  %s\t%s\t%d%s\t%s\t%s\t%s\n",
				cyan.Sprint(t.Subdomain), status, t.Total, errStr, t.SuccessRate, last, from)
		}
		w.Flush()
		fmt.Println()

		return nil
	},
}

func resolveHTTPAddrFromAccount(cmd *cobra.Command, acc *config.Account) string {
	serverFlag, _ := cmd.Flags().GetString("server")
	if serverFlag != "" {
		return serverFlag
	}
	// Default to localhost HTTP API
	return "http://localhost:8080"
}

func formatAgo(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func init() {
	statusCmd.Flags().String("server", "", "HTTP API address (default: http://localhost:8080)")
	rootCmd.AddCommand(statusCmd)
}
