package cmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
)

var (
	uGreen = lipgloss.NewStyle().Foreground(lipgloss.Color("#50fa7b")).Bold(true)
	uRed   = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff5555")).Bold(true)
	uCyan  = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	uDim   = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update pie to the latest version",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Check latest
		var latest string
		var checkErr error
		spinner.New().
			Title("Checking for updates...").
			Action(func() {
				latest, checkErr = getLatestVersion()
			}).Run()

		if checkErr != nil {
			return fmt.Errorf("could not check: %w", checkErr)
		}

		current := strings.TrimPrefix(Version, "v")
		latestClean := strings.TrimPrefix(latest, "v")

		if current == latestClean && current != "dev" {
			fmt.Println(uGreen.Render("  ✓") + " Already on latest version " + uGreen.Render(Version))
			return nil
		}

		fmt.Println(uDim.Render("  Current: ") + Version)
		fmt.Println(uDim.Render("  Latest:  ") + uGreen.Render(latest))
		fmt.Println()

		// Download
		goos := runtime.GOOS
		goarch := runtime.GOARCH
		filename := fmt.Sprintf("pie_%s_%s.tar.gz", goos, goarch)
		url := fmt.Sprintf("https://github.com/pipepie/pipepie/releases/download/%s/%s", latest, filename)

		var dlErr error
		var binary []byte
		spinner.New().
			Title("Downloading "+latest+"...").
			Action(func() {
				binary, dlErr = downloadBinary(url)
			}).Run()

		if dlErr != nil {
			return fmt.Errorf("download failed: %w", dlErr)
		}

		// Find current binary path
		exePath, err := os.Executable()
		if err != nil {
			exePath = "/usr/local/bin/pie"
		}

		// Write
		if err := os.WriteFile(exePath, binary, 0755); err != nil {
			// Try with temp + move
			tmp := exePath + ".new"
			if err := os.WriteFile(tmp, binary, 0755); err != nil {
				return fmt.Errorf("write failed: %w (try: sudo pie update)", err)
			}
			if err := os.Rename(tmp, exePath); err != nil {
				os.Remove(tmp)
				return fmt.Errorf("replace failed: %w (try: sudo pie update)", err)
			}
		}

		fmt.Println(uGreen.Render("  ✓") + " Updated to " + uGreen.Render(latest))
		return nil
	},
}

func downloadBinary(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// tar.gz — extract the "pie" binary
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, err
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name == "pie" || strings.HasSuffix(hdr.Name, "/pie") {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary not found in archive")
}

func init() {
	rootCmd.AddCommand(updateCmd)
}
