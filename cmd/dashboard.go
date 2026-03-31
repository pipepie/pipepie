package cmd

import (
	"encoding/hex"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/config"
	"github.com/Seinarukiro2/pipepie/internal/protocol"
	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/hashicorp/yamux"
	"github.com/spf13/cobra"
)

var (
	dashGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	dashCyan  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	dashDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
	dashRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open dashboard in browser",
	Long: `Authenticates with the server and opens the web dashboard.
Uses saved config from 'pie login'.

The generated link is one-time use and expires in 5 minutes.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, _ := config.LoadClient()
		active := cfg.ActiveAccount()
		if active == nil || active.Server == "" || active.Key == "" {
			return fmt.Errorf("not logged in — run 'pie login' first")
		}

		keyBytes, err := hex.DecodeString(active.Key)
		if err != nil || len(keyBytes) != 32 {
			return fmt.Errorf("invalid key — run 'pie login' again")
		}

		var dashURL string
		var connErr error

		spinner.New().
			Title("Generating dashboard link for "+cfg.Active+"...").
			Action(func() {
				dashURL, connErr = requestDashboardToken(active.Server, keyBytes)
			}).Run()

		if connErr != nil {
			fmt.Println(dashRed.Render("  ✗") + " " + connErr.Error())
			return nil
		}

		fmt.Println(dashGreen.Render("  ✓") + " Dashboard ready")
		fmt.Println()
		fmt.Println("  " + dashCyan.Render(dashURL))
		fmt.Println()
		fmt.Println(dashDim.Render("  Link valid for 5 minutes, one-time use."))
		fmt.Println()

		// Open in browser
		openBrowser(dashURL)

		return nil
	},
}

func requestDashboardToken(serverAddr string, pubKey []byte) (string, error) {
	// Connect to server via Noise NK
	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("cannot reach server: %w", err)
	}
	defer conn.Close()

	encrypted, err := protocol.ClientHandshake(conn, pubKey)
	if err != nil {
		return "", fmt.Errorf("auth failed: %w", err)
	}

	sess, err := yamux.Client(encrypted, yamux.DefaultConfig())
	if err != nil {
		return "", fmt.Errorf("session failed: %w", err)
	}
	defer sess.Close()

	ctrl, err := sess.Open()
	if err != nil {
		return "", fmt.Errorf("open stream: %w", err)
	}

	// Send hello (required by protocol)
	protocol.WriteFrame(ctrl, &pb.Frame{
		Payload: &pb.Frame_Auth{Auth: &pb.Auth{Version: "0.1.0"}},
	})

	// Read auth OK
	frame, err := protocol.ReadFrame(ctrl)
	if err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}
	if frame.GetAuthError() != nil {
		return "", fmt.Errorf("auth rejected: %s", frame.GetAuthError().Message)
	}

	// Request dashboard token
	protocol.WriteFrame(ctrl, &pb.Frame{
		Payload: &pb.Frame_DashTokenReq{DashTokenReq: &pb.DashboardTokenReq{}},
	})

	// Read token response
	frame, err = protocol.ReadFrame(ctrl)
	if err != nil {
		return "", fmt.Errorf("token request failed: %w", err)
	}
	resp := frame.GetDashTokenResp()
	if resp == nil {
		return "", fmt.Errorf("unexpected response from server")
	}

	return resp.Url, nil
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	}
	if cmd != nil {
		cmd.Start()
	}
}

func init() {
	rootCmd.AddCommand(dashboardCmd)
}
