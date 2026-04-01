package cmd

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pipepie/pipepie/internal/config"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	iCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	iGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	iRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
	iYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f1fa8c"))
	iPurple = lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9"))
	iDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
	iBold   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)
	iMono   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2"))
)

var inspectCmd = &cobra.Command{
	Use:   "inspect <request-id>",
	Short: "Inspect a request's full details",
	Long: `Shows headers, body, response, and metadata for a specific request.

  pie inspect abc123-def456
  pie inspect abc123 --subdomain my-app`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		serverHTTP := resolveHTTPAddrFromAccount(cmd, active)
		sub, _ := cmd.Flags().GetString("subdomain")
		if sub == "" && active != nil {
			sub = active.Subdomain
		}

		reqID := args[0]

		// Global lookup — no subdomain needed
		client := &http.Client{Timeout: 5 * time.Second}
		resp, err := client.Get(serverHTTP + "/api/requests/" + reqID)
		if err != nil {
			return fmt.Errorf("cannot reach server: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			return fmt.Errorf("request not found")
		}

		body, _ := io.ReadAll(resp.Body)
		var r struct {
			ID         string  `json:"id"`
			Method     string  `json:"method"`
			Path       string  `json:"path"`
			Query      string  `json:"query"`
			ReqHeaders string  `json:"req_headers"`
			ReqBody    *string `json:"req_body"`
			ReqSize    int     `json:"req_size"`
			Status     string  `json:"status"`
			RespStatus *int    `json:"resp_status"`
			RespHeaders *string `json:"resp_headers"`
			RespBody   *string `json:"resp_body"`
			DurationMs *int64  `json:"duration_ms"`
			Error      *string `json:"error"`
			SourceIP   string  `json:"source_ip"`
			PipelineID string  `json:"pipeline_id"`
			StepName   string  `json:"step_name"`
			TraceID    string  `json:"trace_id"`
			CreatedAt  string  `json:"created_at"`
		}
		json.Unmarshal(body, &r)

		// Method + Path
		mc := iYellow
		switch r.Method {
		case "GET": mc = iGreen
		case "DELETE": mc = iRed
		case "PUT": mc = iCyan
		case "PATCH": mc = iPurple
		}

		fmt.Println()
		fmt.Println("  " + mc.Render(r.Method) + " " + iCyan.Render(r.Path))
		if r.Query != "" {
			fmt.Println("  " + iDim.Render("?"+r.Query))
		}
		fmt.Println()

		// Status
		statusStr := r.Status
		if r.RespStatus != nil {
			code := *r.RespStatus
			if code < 400 {
				statusStr = iGreen.Render(fmt.Sprintf("%d", code))
			} else {
				statusStr = iRed.Render(fmt.Sprintf("%d", code))
			}
		}
		fmt.Println("  " + iDim.Render("Status    ") + statusStr)
		if r.DurationMs != nil {
			fmt.Println("  " + iDim.Render("Duration  ") + fmt.Sprintf("%dms", *r.DurationMs))
		}
		fmt.Println("  " + iDim.Render("Size      ") + fmt.Sprintf("%d bytes", r.ReqSize))
		fmt.Println("  " + iDim.Render("Source    ") + r.SourceIP)
		fmt.Println("  " + iDim.Render("Time      ") + r.CreatedAt)
		if r.PipelineID != "" {
			fmt.Println("  " + iDim.Render("Pipeline  ") + iPurple.Render(r.PipelineID))
		}
		if r.StepName != "" {
			fmt.Println("  " + iDim.Render("Step      ") + iCyan.Render(r.StepName))
		}
		if r.TraceID != "" {
			fmt.Println("  " + iDim.Render("Trace     ") + r.TraceID)
		}
		if r.Error != nil {
			fmt.Println("  " + iDim.Render("Error     ") + iRed.Render(*r.Error))
		}
		fmt.Println("  " + iDim.Render("ID        ") + iDim.Render(r.ID))

		// Request headers
		if r.ReqHeaders != "" {
			fmt.Println()
			fmt.Println("  " + iBold.Render("Request Headers"))
			printHeaders(r.ReqHeaders)
		}

		// Request body
		if r.ReqBody != nil && *r.ReqBody != "" {
			fmt.Println()
			fmt.Println("  " + iBold.Render("Request Body"))
			printBody(*r.ReqBody)
		}

		// Response
		if r.RespStatus != nil {
			if r.RespHeaders != nil && *r.RespHeaders != "" {
				fmt.Println()
				fmt.Println("  " + iBold.Render("Response Headers"))
				printHeaders(*r.RespHeaders)
			}
			if r.RespBody != nil && *r.RespBody != "" {
				fmt.Println()
				fmt.Println("  " + iBold.Render("Response Body"))
				printBody(*r.RespBody)
			}
		}

		fmt.Println()
		fmt.Println("  " + iDim.Render("Replay: ") + iCyan.Render("pie replay "+r.ID+" --subdomain "+sub))
		fmt.Println()
		return nil
	},
}

var replayCmd = &cobra.Command{
	Use:   "replay <request-id>",
	Short: "Replay a webhook request",
	Long: `Replays a captured webhook to the connected tunnel client.

  pie replay abc123-def456
  pie replay abc123 --subdomain my-app`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		serverHTTP := resolveHTTPAddrFromAccount(cmd, active)
		sub, _ := cmd.Flags().GetString("subdomain")
		if sub == "" && active != nil {
			sub = active.Subdomain
		}

		// If no subdomain, look up the request to find its tunnel
		if sub == "" {
			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Get(serverHTTP + "/api/requests/" + args[0])
			if err == nil {
				defer resp.Body.Close()
				var r struct{ TunnelID string `json:"tunnel_id"` }
				json.NewDecoder(resp.Body).Decode(&r)
				// Need subdomain from tunnel_id — for now require flag
			}
			return fmt.Errorf("--subdomain required (tunnel cannot be auto-detected yet)")
		}

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Post(serverHTTP+"/api/tunnels/"+sub+"/requests/"+args[0]+"/replay", "", nil)
		if err != nil {
			return fmt.Errorf("cannot reach server: %w", err)
		}
		defer resp.Body.Close()

		var result struct {
			ReplayID string `json:"replay_id"`
			Status   int    `json:"status"`
			Duration int64  `json:"duration"`
		}
		json.NewDecoder(resp.Body).Decode(&result)

		if resp.StatusCode == 200 {
			statusColor := iGreen
			if result.Status >= 400 {
				statusColor = iRed
			}
			fmt.Println()
			fmt.Println(iGreen.Render("  ✓") + " Replayed → " + statusColor.Render(fmt.Sprintf("%d", result.Status)) + iDim.Render(fmt.Sprintf(" (%dms)", result.Duration)))
			fmt.Println()
		} else {
			body, _ := io.ReadAll(resp.Body)
			fmt.Println(iRed.Render("  ✗") + " " + string(body))
		}
		return nil
	},
}

func printHeaders(raw string) {
	var headers map[string]string
	json.Unmarshal([]byte(raw), &headers)
	for k, v := range headers {
		fmt.Printf("    %s: %s\n", iCyan.Render(k), v)
	}
}

func printBody(raw string) {
	decoded := raw
	if data, err := base64.StdEncoding.DecodeString(raw); err == nil && len(data) > 0 {
		decoded = string(data)
	}
	// Try to pretty-print JSON
	var v any
	if err := json.Unmarshal([]byte(decoded), &v); err == nil {
		pretty, _ := json.MarshalIndent(v, "    ", "  ")
		fmt.Println("    " + string(pretty))
	} else {
		// Truncate if too long
		if len(decoded) > 500 {
			decoded = decoded[:500] + "..."
		}
		fmt.Println("    " + decoded)
	}
}

func init() {
	inspectCmd.Flags().String("subdomain", "", "Tunnel subdomain")
	inspectCmd.Flags().String("server", "", "HTTP API address")
	replayCmd.Flags().String("subdomain", "", "Tunnel subdomain")
	replayCmd.Flags().String("server", "", "HTTP API address")
	rootCmd.AddCommand(inspectCmd)
	rootCmd.AddCommand(replayCmd)
}
