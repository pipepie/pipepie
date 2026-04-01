package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [subdomain]",
	Short: "Stream recent requests for a tunnel",
	Long: `Shows recent webhook requests in real-time.

  pie logs my-app
  pie logs my-app --follow
  pie logs              # uses default subdomain from 'pie login'`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		serverHTTP := resolveHTTPAddrFromAccount(cmd, active)
		follow, _ := cmd.Flags().GetBool("follow")
		showBody, _ := cmd.Flags().GetBool("body")
		limit, _ := cmd.Flags().GetInt("limit")

		subdomain := ""
		if len(args) > 0 {
			subdomain = args[0]
		} else if active != nil && active.Subdomain != "" {
			subdomain = active.Subdomain
		}
		if subdomain == "" {
			return fmt.Errorf("subdomain required — specify as argument or set via 'pie login'")
		}

		green := color.New(color.FgGreen)
		red := color.New(color.FgRed, color.Bold)
		yellow := color.New(color.FgYellow)
		cyan := color.New(color.FgCyan)
		dim := color.New(color.Faint)

		methodColors := map[string]*color.Color{
			"GET": green, "POST": yellow, "PUT": cyan,
			"PATCH": color.New(color.FgMagenta), "DELETE": red,
		}

		seen := make(map[string]bool)

		printRequests := func() error {
			client := &http.Client{Timeout: 5 * time.Second}
			url := fmt.Sprintf("%s/api/tunnels/%s/requests?limit=%d", serverHTTP, subdomain, limit)
			resp, err := client.Get(url)
			if err != nil {
				return err
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
					DurationMs *int64  `json:"duration_ms"`
					StepName   string  `json:"step_name"`
					PipelineID string  `json:"pipeline_id"`
					TraceID    string  `json:"trace_id"`
					ReqBody    *string `json:"req_body"`
					RespBody   *string `json:"resp_body"`
					CreatedAt  string  `json:"created_at"`
				} `json:"requests"`
			}
			json.Unmarshal(body, &data)

			// Print newest first, but only unseen
			for i := len(data.Requests) - 1; i >= 0; i-- {
				r := data.Requests[i]
				if seen[r.ID] {
					continue
				}
				seen[r.ID] = true

				ts := dim.Sprintf("[%s]", formatLogTime(r.CreatedAt))
				mc := methodColors[r.Method]
				if mc == nil {
					mc = dim
				}
				method := mc.Sprintf("%-6s", r.Method)

				status := ""
				if r.Status == "forwarded" && r.RespStatus != nil {
					code := *r.RespStatus
					if code < 400 {
						status = green.Sprintf("%d", code)
					} else {
						status = red.Sprintf("%d", code)
					}
				} else {
					status = dim.Sprint(r.Status)
				}

				dur := dim.Sprint("—")
				if r.DurationMs != nil {
					dur = dim.Sprintf("%dms", *r.DurationMs)
				}

				step := ""
				if r.StepName != "" {
					step = color.New(color.FgCyan).Sprintf(" [%s]", r.StepName)
				}
				trace := ""
				if r.PipelineID != "" {
					trace = color.New(color.FgMagenta).Sprintf(" (%s", r.PipelineID)
					if r.TraceID != "" {
						trace += color.New(color.Faint).Sprintf(":%s", r.TraceID)
					}
					trace += color.New(color.FgMagenta).Sprint(")")
				}

				fmt.Printf("  %s %s %s → %s %s%s%s\n", ts, method, r.Path, status, dur, step, trace)

				if showBody {
					if r.ReqBody != nil && *r.ReqBody != "" {
						bodyStr := decodeBody(*r.ReqBody)
						if bodyStr != "" {
							fmt.Printf("    %s %s\n", dim.Sprint("→"), truncate(formatJSON(bodyStr), 200))
						}
					}
					if r.RespBody != nil && *r.RespBody != "" {
						bodyStr := decodeBody(*r.RespBody)
						if bodyStr != "" {
							fmt.Printf("    %s %s\n", dim.Sprint("←"), truncate(formatJSON(bodyStr), 200))
						}
					}
				}
			}
			return nil
		}

		// Initial fetch
		if err := printRequests(); err != nil {
			red.Printf("  Cannot reach server: %v\n", err)
			return nil
		}

		if !follow {
			return nil
		}

		// Follow mode — poll every 1s
		dim.Printf("  Following %s... (Ctrl+C to stop)\n\n", subdomain)
		for {
			time.Sleep(1 * time.Second)
			printRequests()
		}
	},
}

func formatLogTime(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	return t.Local().Format("15:04:05")
}

func decodeBody(s string) string {
	// API returns body as base64-encoded bytes or raw string
	if s == "" {
		return ""
	}
	// Try base64 decode
	if data, err := base64.StdEncoding.DecodeString(s); err == nil && len(data) > 0 {
		return string(data)
	}
	return s
}

func formatJSON(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		compact, _ := json.Marshal(v) // compact single line
		return string(compact)
	}
	return s
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func init() {
	logsCmd.Flags().BoolP("follow", "f", false, "Follow mode (stream new requests)")
	logsCmd.Flags().BoolP("body", "b", false, "Show request/response bodies")
	logsCmd.Flags().Int("limit", 20, "Number of recent requests to show")
	logsCmd.Flags().String("server", "", "HTTP API address")
	rootCmd.AddCommand(logsCmd)
}
