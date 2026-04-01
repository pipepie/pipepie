package cmd

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/pipepie/pipepie/internal/client"
	"github.com/pipepie/pipepie/internal/config"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start all tunnels from pipepie.yaml",
	Long: `Reads pipepie.yaml from the current directory and starts all defined tunnels.

Example pipepie.yaml:

  server: tunnel.mysite.com:9443
  key: a7f3bc21...

  tunnels:
    api:
      subdomain: myapi
      forward: http://localhost:3000
    frontend:
      subdomain: myapp
      port: 5173

  pipeline:
    name: image-gen
    steps:
      - name: replicate-sdxl
        webhook: /replicate
        forward: localhost:3000/on-image
      - name: fal-upscale
        webhook: /fal
        forward: localhost:3000/on-upscale`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")

		var cfg *config.File
		var err error
		if cfgPath != "" {
			cfg, err = config.Load(cfgPath)
		} else {
			path, findErr := config.Find()
			if findErr != nil {
				return findErr
			}
			cfg, err = config.Load(path)
			cfgPath = path
		}
		if err != nil {
			return err
		}

		printUpBanner(cfgPath, cfg)

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()

		keyBytes, err := hex.DecodeString(cfg.Key)
		if err != nil || len(keyBytes) != 32 {
			return fmt.Errorf("invalid key in pipepie.yaml")
		}

		// Send pipeline rules to server if defined
		if rules := cfg.PipelineRules(); len(rules) > 0 {
			go func() {
				time.Sleep(2 * time.Second) // wait for tunnels to connect
				rulesJSON, _ := json.Marshal(rules)
				// Derive HTTP address from tunnel server
				host := cfg.Server
				if i := strings.Index(host, ":"); i != -1 {
					host = host[:i]
				}
				for _, port := range []string{"443", "8080", "80"} {
					resp, err := http.Post("http://"+host+":"+port+"/api/pipeline-rules", "application/json", bytes.NewReader(rulesJSON))
					if err == nil {
						resp.Body.Close()
						if resp.StatusCode == 200 {
							break
						}
					}
				}
			}()
		}

		tunnels := cfg.ResolvedTunnels()
		var wg sync.WaitGroup
		errCh := make(chan error, len(tunnels))

		for name, t := range tunnels {
			wg.Add(1)
			go func(name string, t config.Tunnel) {
				defer wg.Done()
				c := client.New(client.Config{
					ServerAddr:   cfg.Server,
					ServerPubKey: keyBytes,
					Subdomain:    t.Subdomain,
					Forward:      t.Forward,
				})
				if err := c.Run(ctx); err != nil && ctx.Err() == nil {
					errCh <- fmt.Errorf("[%s] %w", name, err)
				}
			}(name, t)
		}

		// Wait for interrupt or error
		go func() {
			wg.Wait()
			close(errCh)
		}()

		select {
		case <-ctx.Done():
			fmt.Println()
			color.New(color.Faint).Println("  Shutting down...")
			return nil
		case err := <-errCh:
			if err != nil {
				return err
			}
			return nil
		}
	},
}

func printUpBanner(path string, cfg *config.File) {
	bold := color.New(color.Bold)
	cyan := color.New(color.FgCyan, color.Bold)
	dim := color.New(color.Faint)
	green := color.New(color.FgGreen)
	yellow := color.New(color.FgYellow)

	cyan.Println("\n  ╭─────────────────────────────────╮")
	cyan.Print("  │  ")
	bold.Print("pipepie up")
	dim.Print("              ")
	cyan.Println("    │")
	cyan.Println("  ╰─────────────────────────────────╯")
	fmt.Println()

	dim.Printf("  Config  %s\n", path)
	dim.Printf("  Server  %s\n", cfg.Server)
	fmt.Println()

	tunnels := cfg.ResolvedTunnels()
	for name, t := range tunnels {
		green.Printf("  ● %s", name)
		dim.Printf("  %s → %s\n", t.Subdomain, t.Forward)
	}

	if p := cfg.Pipeline; p != nil {
		fmt.Println()
		yellow.Printf("  ⚡ Pipeline: %s\n", p.Name)
		for i, s := range p.Steps {
			arrow := "├"
			if i == len(p.Steps)-1 {
				arrow = "└"
			}
			dim.Printf("  %s─ %s", arrow, s.Name)
			dim.Printf("  %s → %s\n", s.Webhook, s.Forward)
		}
	}
	fmt.Println()
}

func init() {
	upCmd.Flags().StringP("config", "c", "", "Path to pipepie.yaml (default: auto-detect)")
	rootCmd.AddCommand(upCmd)
}
