package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	tPurple = lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true)
	tGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	tRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
	tCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	tDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
	tYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f1fa8c"))
	tBold   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)
)

var tracesCmd = &cobra.Command{
	Use:   "traces [subdomain]",
	Short: "Show pipeline traces",
	Long: `Shows AI pipeline trace timelines from the terminal.

  pie traces my-app
  pie traces            # uses default subdomain`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		serverHTTP := resolveHTTPAddrFromAccount(cmd, active)
		limit, _ := cmd.Flags().GetInt("limit")

		subdomain := ""
		if len(args) > 0 {
			subdomain = args[0]
		} else if active != nil && active.Subdomain != "" {
			subdomain = active.Subdomain
		}
		if subdomain == "" {
			return fmt.Errorf("subdomain required")
		}

		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(fmt.Sprintf("%s/api/tunnels/%s/requests?limit=%d", serverHTTP, subdomain, limit))
		if err != nil {
			return fmt.Errorf("cannot reach server: %w", err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var data struct {
			Requests []struct {
				ID         string `json:"id"`
				Method     string `json:"method"`
				Path       string `json:"path"`
				Status     string `json:"status"`
				RespStatus *int   `json:"resp_status"`
				DurationMs *int64 `json:"duration_ms"`
				StepName   string `json:"step_name"`
				PipelineID string `json:"pipeline_id"`
				TraceID    string `json:"trace_id"`
				CreatedAt  string `json:"created_at"`
			} `json:"requests"`
		}
		json.Unmarshal(body, &data)

		// Group by trace_id
		traceMap := make(map[string][]int)
		for i, r := range data.Requests {
			if r.TraceID != "" {
				traceMap[r.TraceID] = append(traceMap[r.TraceID], i)
			}
		}

		if len(traceMap) == 0 {
			fmt.Println()
			fmt.Println(tDim.Render("  No pipeline traces found."))
			fmt.Println(tDim.Render("  Webhooks from Replicate, fal.ai, RunPod are auto-detected."))
			fmt.Println(tDim.Render("  Or add X-Pipepie-Pipeline headers manually."))
			fmt.Println()
			return nil
		}

		fmt.Println()
		for traceID, indices := range traceMap {
			first := data.Requests[indices[len(indices)-1]] // oldest
			pipeline := first.PipelineID
			if pipeline == "" {
				pipeline = "pipeline"
			}

			// Check if any step failed
			failed := false
			totalMs := int64(0)
			maxMs := int64(1)
			for _, idx := range indices {
				r := data.Requests[idx]
				if r.Status == "failed" || r.Status == "timeout" {
					failed = true
				}
				if r.DurationMs != nil {
					totalMs += *r.DurationMs
					if *r.DurationMs > maxMs {
						maxMs = *r.DurationMs
					}
				}
			}

			// Header
			status := tGreen.Render("OK")
			if failed {
				status = tRed.Render("FAILED")
			}
			fmt.Printf("  %s %s %s %s\n", tPurple.Render(pipeline), tDim.Render(traceID), status, tDim.Render(fmt.Sprintf("%dms", totalMs)))

			// Timeline bars
			for i := len(indices) - 1; i >= 0; i-- {
				r := data.Requests[indices[i]]
				name := r.StepName
				if name == "" {
					name = r.Path
				}

				dur := int64(0)
				if r.DurationMs != nil {
					dur = *r.DurationMs
				}

				// Bar width (proportional)
				barWidth := int(float64(dur) / float64(maxMs) * 30)
				if barWidth < 2 {
					barWidth = 2
				}

				barColor := tGreen
				marker := "█"
				if r.Status == "failed" || r.Status == "timeout" {
					barColor = tRed
				}
				if r.Status == "pending" {
					barColor = tYellow
					marker = "░"
				}

				bar := barColor.Render(strings.Repeat(marker, barWidth))
				durStr := tDim.Render(fmt.Sprintf("%dms", dur))
				statusStr := ""
				if r.RespStatus != nil {
					if *r.RespStatus >= 400 {
						statusStr = " " + tRed.Render(fmt.Sprintf("%d", *r.RespStatus))
					} else {
						statusStr = " " + tGreen.Render(fmt.Sprintf("%d", *r.RespStatus))
					}
				}

				fmt.Printf("    %s %s %s%s\n", tCyan.Render(fmt.Sprintf("%-15s", name)), bar, durStr, statusStr)
			}
			fmt.Println()
		}
		return nil
	},
}

func init() {
	tracesCmd.Flags().Int("limit", 50, "Number of requests to scan for traces")
	tracesCmd.Flags().String("server", "", "HTTP API address")
	rootCmd.AddCommand(tracesCmd)
}
