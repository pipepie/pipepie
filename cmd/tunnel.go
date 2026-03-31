package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var tunnelCmd = &cobra.Command{
	Use:   "tunnel",
	Short: "Manage tunnels (create, list, delete)",
}

var tunnelCreateCmd = &cobra.Command{
	Use:   "create <subdomain>",
	Short: "Create a new tunnel",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serverURL, _ := cmd.Flags().GetString("server")

		body, _ := json.Marshal(map[string]string{"subdomain": args[0]})
		resp, err := http.Post(serverURL+"/api/admin/tunnels", "application/json", bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("read response: %w", err)
		}
		if resp.StatusCode != http.StatusCreated {
			return fmt.Errorf("server: %s", string(data))
		}

		var result struct {
			Subdomain string `json:"subdomain"`
			URL       string `json:"url"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		green := color.New(color.FgGreen, color.Bold)
		dim := color.New(color.Faint)
		cyan := color.New(color.FgCyan)

		fmt.Println()
		green.Println("  ✓ Tunnel created!")
		fmt.Println()
		dim.Print("  Subdomain  ")
		fmt.Println(result.Subdomain)
		dim.Print("  URL        ")
		cyan.Println(result.URL)
		fmt.Println()

		return nil
	},
}

var tunnelListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all tunnels",
	RunE: func(cmd *cobra.Command, args []string) error {
		serverURL, _ := cmd.Flags().GetString("server")

		resp, err := http.Get(serverURL + "/api/admin/tunnels")
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		var tunnels []struct {
			Subdomain string `json:"subdomain"`
			Online    bool   `json:"online"`
			CreatedAt string `json:"created_at"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tunnels); err != nil {
			return fmt.Errorf("parse response: %w", err)
		}

		if len(tunnels) == 0 {
			color.New(color.Faint).Println("  No tunnels found.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		color.New(color.Bold).Fprintf(w, "  SUBDOMAIN\tSTATUS\tCREATED\n")
		for _, t := range tunnels {
			status := color.RedString("offline")
			if t.Online {
				status = color.GreenString("online")
			}
			fmt.Fprintf(w, "  %s\t%s\t%s\n", t.Subdomain, status, t.CreatedAt[:10])
		}
		w.Flush()
		fmt.Println()
		return nil
	},
}

var tunnelDeleteCmd = &cobra.Command{
	Use:   "delete <subdomain>",
	Short: "Delete a tunnel",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		serverURL, _ := cmd.Flags().GetString("server")

		req, _ := http.NewRequest("DELETE", serverURL+"/api/admin/tunnels/"+args[0], nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("request failed: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			data, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("server: %s", string(data))
		}

		color.New(color.FgGreen, color.Bold).Printf("  ✓ Tunnel %q deleted.\n\n", args[0])
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{tunnelCreateCmd, tunnelListCmd, tunnelDeleteCmd} {
		c.Flags().String("server", "http://localhost:8080", "Server HTTP address")
	}
	tunnelCmd.AddCommand(tunnelCreateCmd, tunnelListCmd, tunnelDeleteCmd)
	rootCmd.AddCommand(tunnelCmd)
}
