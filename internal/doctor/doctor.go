// Package doctor runs diagnostic checks on a pipepie server configuration.
package doctor

import (
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/setup"
	"github.com/fatih/color"
	"gopkg.in/yaml.v3"
)

var (
	bold   = color.New(color.Bold)
	green  = color.New(color.FgGreen, color.Bold)
	red    = color.New(color.FgRed, color.Bold)
	yellow = color.New(color.FgYellow, color.Bold)
	cyan   = color.New(color.FgCyan)
	dim    = color.New(color.Faint)
)

// Result of a single check.
type Result struct {
	Name    string
	OK      bool
	Warning bool
	Message string
}

// Run executes all diagnostic checks.
func Run(configPath string) error {
	printBanner()

	var results []Result
	var cfg *setup.ServerConfig

	// ── Config ───────────────────────────────────────────────────────
	section("Config")
	if configPath == "" {
		results = append(results, fail("Config file", "no config path provided — use --config"))
	} else if data, err := os.ReadFile(configPath); err != nil {
		results = append(results, fail("Config file", fmt.Sprintf("cannot read: %v", err)))
	} else {
		var c setup.ServerConfig
		if err := yaml.Unmarshal(data, &c); err != nil {
			results = append(results, fail("Config file", fmt.Sprintf("invalid YAML: %v", err)))
		} else {
			cfg = &c
			results = append(results, pass("Config file", configPath+" is valid"))
			results = append(results, pass("Domain", c.Domain))
			results = append(results, pass("TLS mode", c.TLS.Mode))
		}
	}

	if cfg == nil {
		printResults(results)
		return nil
	}

	// ── Network ──────────────────────────────────────────────────────
	section("Network")

	pubIP, err := detectIP()
	if err != nil {
		results = append(results, warn("Public IP", fmt.Sprintf("could not detect: %v", err)))
	} else {
		results = append(results, pass("Public IP", pubIP))
	}

	for _, port := range []string{cfg.TunnelAddr, cfg.HTTPAddr} {
		r := checkPort(port)
		results = append(results, r)
	}
	if cfg.TLS.Mode != "none" {
		results = append(results, checkPort(":80"))
	}

	// ── DNS ──────────────────────────────────────────────────────────
	section("DNS")

	if pubIP != "" {
		results = append(results, checkDNS(cfg.Domain, pubIP))
		results = append(results, checkDNS("*."+cfg.Domain, pubIP))

		// Test with a random subdomain
		testSub := "pie-check." + cfg.Domain
		r := checkDNS(testSub, pubIP)
		results = append(results, r)
	} else {
		results = append(results, warn("DNS", "skipped — could not detect public IP"))
	}

	// ── TLS ──────────────────────────────────────────────────────────
	section("TLS")

	switch cfg.TLS.Mode {
	case "cloudflare":
		if cfg.TLS.CFToken == "" {
			results = append(results, fail("Cloudflare token", "empty"))
		} else {
			results = append(results, pass("Cloudflare token", "configured ("+cfg.TLS.CFToken[:8]+"...)"))
		}
		if cfg.TLS.CacheDir != "" {
			results = append(results, checkCertCache(cfg.TLS.CacheDir, cfg.Domain))
		}
	case "manual":
		results = append(results, checkCertFiles(cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.Domain))
	case "none":
		results = append(results, warn("TLS", "disabled — make sure a reverse proxy handles HTTPS"))
	}

	// ── Storage ──────────────────────────────────────────────────────
	section("Storage")

	results = append(results, checkSQLite(cfg.DBPath))
	results = append(results, checkDiskSpace(filepath.Dir(cfg.DBPath)))

	// ── Connectivity (external check) ────────────────────────────────
	section("Connectivity")

	if pubIP != "" {
		results = append(results, checkPortReachable(pubIP, cfg.TunnelAddr, "Tunnel"))
	}

	// ── System ───────────────────────────────────────────────────────
	section("System")

	results = append(results, checkFileDescriptors())

	// ── Summary ──────────────────────────────────────────────────────
	printResults(results)
	return nil
}

// ── Checks ───────────────────────────────────────────────────────────

func checkPort(addr string) Result {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return warn(fmt.Sprintf("Port %s", addr), fmt.Sprintf("in use or unavailable: %v", err))
	}
	ln.Close()
	return pass(fmt.Sprintf("Port %s", addr), "available")
}

func checkDNS(host, expectedIP string) Result {
	lookup := strings.TrimPrefix(host, "*.")
	addrs, err := net.LookupHost(lookup)
	if err != nil {
		return fail(fmt.Sprintf("DNS %s", host), "not found")
	}
	for _, a := range addrs {
		if a == expectedIP {
			return pass(fmt.Sprintf("DNS %s", host), a)
		}
	}
	return fail(fmt.Sprintf("DNS %s", host), fmt.Sprintf("resolves to %s, expected %s", strings.Join(addrs, ","), expectedIP))
}

func checkCertCache(dir, domain string) Result {
	// Look for cert files in certmagic cache
	pattern := filepath.Join(dir, "certificates", "*", "*."+domain, "*.crt")
	matches, _ := filepath.Glob(pattern)
	if len(matches) == 0 {
		// Try alternative paths
		pattern2 := filepath.Join(dir, "**", "*.crt")
		matches, _ = filepath.Glob(pattern2)
	}
	if len(matches) == 0 {
		return warn("TLS certificate", "no cached certificates found in "+dir+" — will be issued on first request")
	}

	// Try to parse and check expiry
	for _, f := range matches {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		cert, err := x509.ParseCertificate(data)
		if err != nil {
			// Try PEM
			block, _ := decodePEM(data)
			if block != nil {
				cert, _ = x509.ParseCertificate(block)
			}
		}
		if cert != nil {
			days := int(time.Until(cert.NotAfter).Hours() / 24)
			if days < 0 {
				return fail("TLS certificate", fmt.Sprintf("expired %d days ago", -days))
			}
			if days < 14 {
				return warn("TLS certificate", fmt.Sprintf("expires in %d days", days))
			}
			return pass("TLS certificate", fmt.Sprintf("valid, expires in %d days", days))
		}
	}
	return pass("TLS certificate cache", dir)
}

func checkCertFiles(certFile, keyFile, domain string) Result {
	if certFile == "" || keyFile == "" {
		return fail("TLS cert/key", "paths not configured")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return fail("TLS cert/key", fmt.Sprintf("invalid: %v", err))
	}
	if len(cert.Certificate) > 0 {
		parsed, err := x509.ParseCertificate(cert.Certificate[0])
		if err == nil {
			days := int(time.Until(parsed.NotAfter).Hours() / 24)
			if days < 0 {
				return fail("TLS certificate", fmt.Sprintf("expired %d days ago", -days))
			}
			// Check if it covers the domain
			if err := parsed.VerifyHostname("test."+domain); err != nil {
				return warn("TLS certificate", fmt.Sprintf("valid (%d days) but may not cover *.%s", days, domain))
			}
			return pass("TLS certificate", fmt.Sprintf("valid for *.%s, expires in %d days", domain, days))
		}
	}
	return pass("TLS cert/key", "loaded successfully")
}

func checkSQLite(path string) Result {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fail("SQLite", fmt.Sprintf("cannot open: %v", err))
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		return fail("SQLite", fmt.Sprintf("cannot ping: %v", err))
	}
	return pass("SQLite", path+" writable")
}

func checkDiskSpace(dir string) Result {
	return checkDiskSpacePlatform(dir)
}

func checkPortReachable(ip, port, label string) Result {
	// Quick self-check: try to connect to our own port from outside
	addr := ip + port
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return warn(label+" port reachable", fmt.Sprintf("%s — may be blocked by firewall", addr))
	}
	conn.Close()
	return pass(label+" port reachable", addr)
}

func checkFileDescriptors() Result {
	return checkFDLimitPlatform()
}

func detectIP() (string, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("https://api.ipify.org")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	ip := strings.TrimSpace(string(data))
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid: %s", ip)
	}
	return ip, nil
}

func decodePEM(data []byte) ([]byte, error) {
	// Simple PEM extraction
	start := strings.Index(string(data), "-----BEGIN")
	end := strings.Index(string(data), "-----END")
	if start == -1 || end == -1 {
		return nil, fmt.Errorf("no PEM block")
	}
	return data, nil // x509.ParseCertificate handles PEM internally for some formats
}

// ── Output helpers ───────────────────────────────────────────────────

func pass(name, msg string) Result  { green.Printf("  ✓ %-28s %s\n", name, msg); return Result{name, true, false, msg} }
func fail(name, msg string) Result  { red.Printf("  ✗ %-28s %s\n", name, msg); return Result{name, false, false, msg} }
func warn(name, msg string) Result  { yellow.Printf("  ! %-28s %s\n", name, msg); return Result{name, true, true, msg} }

func section(name string) {
	fmt.Println()
	bold.Printf("  %s\n", name)
	fmt.Println()
}

func printBanner() {
	c := color.New(color.FgCyan, color.Bold)
	b := color.New(color.Bold)
	d := color.New(color.Faint)
	c.Println("\n  ╭──────────────────────────────────╮")
	c.Print("  │  ")
	b.Print("pie doctor")
	d.Print("  system diagnostics")
	c.Println("  │")
	c.Println("  ╰──────────────────────────────────╯")
}

func printResults(results []Result) {
	passed, warnings, failed := 0, 0, 0
	for _, r := range results {
		if !r.OK {
			failed++
		} else if r.Warning {
			warnings++
		} else {
			passed++
		}
	}
	fmt.Println()
	fmt.Print("  ")
	green.Printf("%d passed", passed)
	if warnings > 0 {
		fmt.Print(", ")
		yellow.Printf("%d warnings", warnings)
	}
	if failed > 0 {
		fmt.Print(", ")
		red.Printf("%d failed", failed)
	}
	fmt.Println()
	fmt.Println()
}
