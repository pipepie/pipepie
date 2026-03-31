package client

import (
	"fmt"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	sGreen  = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	sRed    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
	sYellow = lipgloss.NewStyle().Foreground(lipgloss.Color("#f1fa8c"))
	sCyan   = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	sPink   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true)
	sDim    = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
	sBold   = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2")).Bold(true)
	sBlue   = lipgloss.NewStyle().Foreground(lipgloss.Color("#60a5fa"))
	sMagenta = lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9"))

	urlBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#50fa7b")).
		Padding(0, 2)

	methodStyles = map[string]lipgloss.Style{
		"GET":    lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true),
		"POST":   lipgloss.NewStyle().Foreground(lipgloss.Color("#f1fa8c")).Bold(true),
		"PUT":    lipgloss.NewStyle().Foreground(lipgloss.Color("#60a5fa")).Bold(true),
		"PATCH":  lipgloss.NewStyle().Foreground(lipgloss.Color("#bd93f9")).Bold(true),
		"DELETE": lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true),
	}
)

// Display handles colored terminal output.
type Display struct {
	verbose bool
}

// NewDisplay creates a new Display.
func NewDisplay() *Display { return &Display{} }

// Connected prints the connection banner — clean and focused.
func (d *Display) Connected(publicURL, forward string) {
	fmt.Println()
	fmt.Println(urlBox.Render(
		sGreen.Render("✓ ") + sCyan.Render(publicURL) + sDim.Render(" → ") + forward,
	))
	fmt.Println()
	fmt.Println(sDim.Render("  Encrypted tunnel ready. Waiting for requests..."))
	fmt.Println()
}

// ConnectedVerbose prints detailed connection info.
func (d *Display) ConnectedVerbose(publicURL, forward string) {
	fmt.Println()
	fmt.Println(urlBox.Render(
		sGreen.Render("✓ ") + sCyan.Render(publicURL) + sDim.Render(" → ") + forward,
	))
	fmt.Println()
	fmt.Println(sDim.Render("  Protocol   ") + sBold.Render("Noise NK + yamux + Protobuf + zstd"))
	fmt.Println(sDim.Render("  Encryption ") + sBold.Render("ChaChaPoly + BLAKE2b (end-to-end)"))
	fmt.Println()
	fmt.Println(sDim.Render("  Waiting for requests..."))
	fmt.Println()
}

// Request prints a forwarded request with size info.
func (d *Display) Request(method, path string, status int, dur time.Duration, replay bool) {
	ts := sDim.Render(time.Now().Format("15:04:05"))
	m := fmtMethod(method)
	s := fmtStatus(status)
	ds := sDim.Render(fmtDuration(dur))

	replay_mark := ""
	if replay {
		replay_mark = sYellow.Render(" ↻")
	}

	fmt.Printf("  %s  %s %s %s %s%s\n", ts, m, path, s, ds, replay_mark)
}

// RequestWithSize prints a request with body size.
func (d *Display) RequestWithSize(method, path string, status int, dur time.Duration, bodySize int, replay bool) {
	ts := sDim.Render(time.Now().Format("15:04:05"))
	m := fmtMethod(method)
	s := fmtStatus(status)
	ds := sDim.Render(fmtDuration(dur))
	sz := ""
	if bodySize > 0 {
		sz = sDim.Render(" " + fmtSize(bodySize))
	}

	replay_mark := ""
	if replay {
		replay_mark = sYellow.Render(" ↻")
	}

	fmt.Printf("  %s  %s %s %s %s%s%s\n", ts, m, path, s, ds, sz, replay_mark)
}

// Error prints a failed request.
func (d *Display) Error(method, path string, err error) {
	ts := sDim.Render(time.Now().Format("15:04:05"))
	fmt.Printf("  %s  %s %s %s\n", ts, fmtMethod(method), path, sRed.Render(err.Error()))
}

// TCPConnection prints a TCP proxy event.
func (d *Display) TCPConnection(localAddr string) {
	ts := sDim.Render(time.Now().Format("15:04:05"))
	fmt.Printf("  %s  %s %s\n", ts, sCyan.Render("⇄ TCP →"), localAddr)
}

// Reconnecting prints reconnection info.
func (d *Display) Reconnecting(attempt int, delay time.Duration, err error) {
	msg := sYellow.Render(fmt.Sprintf("  ⟳ Reconnecting in %s", delay.Round(time.Second)))
	if err != nil {
		msg += sDim.Render(fmt.Sprintf(" — %v", err))
	}
	fmt.Println(msg)
}

// AuthBlocked prints when --auth blocks a request.
func (d *Display) AuthBlocked(method, path string) {
	ts := sDim.Render(time.Now().Format("15:04:05"))
	fmt.Printf("  %s  %s %s %s\n", ts, fmtMethod(method), path, sRed.Render("401 blocked"))
}

// NotLoggedIn prints a helpful error when no account is configured.
func NotLoggedIn() {
	fmt.Println()
	fmt.Println(sRed.Render("  Not logged in"))
	fmt.Println()
	fmt.Println(sDim.Render("  Connect to a server first:"))
	fmt.Println()
	fmt.Println(sCyan.Render("    pie login --server <host>:9443 --key <server-key>"))
	fmt.Println()
	fmt.Println(sDim.Render("  Get the server key from ") + sCyan.Render("pie setup") + sDim.Render(" on your server."))
	fmt.Println()
}

// ── formatters ───────────────────────────────────────────────────────

func fmtMethod(m string) string {
	if s, ok := methodStyles[m]; ok {
		return s.Render(fmt.Sprintf("%-7s", m))
	}
	return fmt.Sprintf("%-7s", m)
}

func fmtStatus(code int) string {
	s := fmt.Sprintf("%d", code)
	switch {
	case code >= 200 && code < 300:
		return sGreen.Render(s)
	case code >= 300 && code < 400:
		return sCyan.Render(s)
	case code >= 400 && code < 500:
		return sYellow.Render(s)
	default:
		return sRed.Render(s)
	}
}

func fmtDuration(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func fmtSize(bytes int) string {
	switch {
	case bytes < 1024:
		return fmt.Sprintf("%dB", bytes)
	case bytes < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
	}
}
