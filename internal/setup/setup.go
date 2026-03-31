// Package setup provides the interactive server setup wizard.
package setup

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/protocol"
	"github.com/caddyserver/certmagic"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/huh/spinner"
	"github.com/charmbracelet/lipgloss"
	"github.com/libdns/cloudflare"
	"gopkg.in/yaml.v3"
)

// ── Dracula theme ────────────────────────────────────────────────────

var (
	purple    = lipgloss.Color("#bd93f9")
	pink      = lipgloss.Color("#ff79c6")
	green     = lipgloss.Color("#50fa7b")
	red       = lipgloss.Color("#ff5555")
	yellow    = lipgloss.Color("#f1fa8c")
	cyan      = lipgloss.Color("#8be9fd")
	fg        = lipgloss.Color("#f8f8f2")
	comment   = lipgloss.Color("#6272a4")
	bg        = lipgloss.Color("#282a36")

	titleStyle   = lipgloss.NewStyle().Foreground(purple).Bold(true)
	successStyle = lipgloss.NewStyle().Foreground(green).Bold(true)
	errorStyle   = lipgloss.NewStyle().Foreground(red).Bold(true)
	warnStyle    = lipgloss.NewStyle().Foreground(yellow).Bold(true)
	dimStyle     = lipgloss.NewStyle().Foreground(comment)
	cyanStyle    = lipgloss.NewStyle().Foreground(cyan)
	boldStyle    = lipgloss.NewStyle().Foreground(fg).Bold(true)
	keyStyle     = lipgloss.NewStyle().Foreground(pink).Bold(true)

	bannerStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(purple).
			Padding(0, 2).
			Foreground(fg)

	sectionStyle = lipgloss.NewStyle().
			Foreground(purple).
			Bold(true).
			MarginTop(1).
			MarginBottom(0)
)

func draculaTheme() *huh.Theme {
	t := huh.ThemeDracula()
	return t
}

// ServerConfig is written to pipepie.yaml.
type ServerConfig struct {
	Domain     string    `yaml:"domain"`
	TunnelAddr string    `yaml:"tunnel_addr"`
	HTTPAddr   string    `yaml:"http_addr"`
	DBPath     string    `yaml:"db_path"`
	KeyFile    string    `yaml:"key_file"`
	TLS        TLSConfig `yaml:"tls"`
}

type TLSConfig struct {
	Mode     string `yaml:"mode"`
	CertFile string `yaml:"cert_file,omitempty"`
	KeyFile  string `yaml:"key_file,omitempty"`
	CFToken  string `yaml:"cf_token,omitempty"`
	CacheDir string `yaml:"cache_dir,omitempty"`
}

// Run executes the interactive setup wizard.
func Run(configPath string) error {
	fmt.Println()
	fmt.Println("  " + titleStyle.Render("pie setup") + "  " + dimStyle.Render("server configuration"))
	fmt.Println()

	// Kill existing pie server if running (suppress output)
	exec.Command("systemctl", "stop", "pipepie").CombinedOutput()
	exec.Command("pkill", "-f", "pie server").CombinedOutput()
	time.Sleep(500 * time.Millisecond)

	// ── 1. Domain ────────────────────────────────────────────────────
	var domain string
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Domain").
				Description("Your server domain (e.g. tunnel.mysite.com)").
				Placeholder("tunnel.mysite.com").
				Value(&domain).
				Validate(func(s string) error {
					if s == "" {
						return fmt.Errorf("domain is required")
					}
					if !strings.Contains(s, ".") {
						return fmt.Errorf("must be a valid domain")
					}
					return nil
				}),
		),
	).WithTheme(draculaTheme()).Run()
	if err != nil {
		return err
	}
	domain = strings.TrimSpace(domain)

	// ── 2. Public IP ─────────────────────────────────────────────────
	var pubIP string
	spinner.New().
		Title("Detecting public IP...").
		Action(func() {
			pubIP, _ = detectPublicIP()
		}).Run()

	if pubIP == "" {
		err := huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Public IP").
					Description("Could not detect automatically").
					Value(&pubIP),
			),
		).WithTheme(draculaTheme()).Run()
		if err != nil {
			return err
		}
	} else {
		fmt.Println(successStyle.Render("  ✓") + " Public IP: " + boldStyle.Render(pubIP))
	}

	// ── 3. DNS ───────────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(sectionStyle.Render("  DNS Records"))
	fmt.Println()

	baseOK := checkDNS(domain, pubIP)
	wildOK := checkDNS("*."+domain, pubIP)

	if !baseOK || !wildOK {
		fmt.Println()
		fmt.Println(warnStyle.Render("  Add these DNS records at your registrar:"))
		fmt.Println()
		if !baseOK {
			fmt.Printf("    %s  A  %s\n", cyanStyle.Render(fmt.Sprintf("%-35s", domain)), pubIP)
		}
		if !wildOK {
			fmt.Printf("    %s  A  %s\n", cyanStyle.Render(fmt.Sprintf("%-35s", "*."+domain)), pubIP)
		}
		fmt.Println()

		var ready bool
		huh.NewForm(
			huh.NewGroup(
				huh.NewConfirm().
					Title("DNS records added?").
					Value(&ready),
			),
		).WithTheme(draculaTheme()).Run()

		if ready {
			spinner.New().
				Title("Waiting for DNS propagation...").
				Action(func() {
					for range 20 {
						time.Sleep(2 * time.Second)
						if checkDNS(domain, pubIP) && checkDNS("check."+domain, pubIP) {
							return
						}
					}
				}).Run()
		}
	}

	// ── 4. Port check + nginx ────────────────────────────────────────
	fmt.Println()
	fmt.Println(sectionStyle.Render("  Ports & Web Server"))
	fmt.Println()

	behindNginx := false
	port443busy := isPortBusy(443)
	nginxInstalled := isNginxInstalled()

	if port443busy && nginxInstalled {
		fmt.Println(warnStyle.Render("  ✗") + " Port 443 in use by " + boldStyle.Render("nginx"))
		fmt.Println()

		var nginxChoice string
		huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("nginx detected on port 443").
					Options(
						huh.NewOption("Auto-configure nginx as reverse proxy", "auto"),
						huh.NewOption("I'll stop nginx — pipepie handles TLS", "stop"),
					).
					Value(&nginxChoice),
			),
		).WithTheme(draculaTheme()).Run()

		if nginxChoice == "auto" {
			behindNginx = true
			fmt.Println(successStyle.Render("  →") + " pipepie on :8080, nginx proxies :443")
		} else {
			fmt.Println(dimStyle.Render("  Run: sudo systemctl stop nginx"))
			var stopped bool
			huh.NewForm(huh.NewGroup(huh.NewConfirm().Title("nginx stopped?").Value(&stopped))).WithTheme(draculaTheme()).Run()
		}
	} else if port443busy {
		fmt.Println(warnStyle.Render("  ✗") + " Port 443 in use by another process")
		behindNginx = true
	} else {
		fmt.Println(successStyle.Render("  ✓") + " Port 443 available")
	}

	if !isPortBusy(9443) {
		fmt.Println(successStyle.Render("  ✓") + " Port 9443 available")
	} else {
		fmt.Println(warnStyle.Render("  ✗") + " Port 9443 in use")
	}

	// Firewall — only check if a firewall is actually active
	fw := detectFirewall()
	if fw == "ufw" || fw == "firewalld" {
		if behindNginx {
		}
		fmt.Println()
		fmt.Println(dimStyle.Render("  Firewall detected: " + fw))
		fmt.Println(dimStyle.Render("  Make sure ports 80, 443, 9443 are open."))
	}

	// ── 5. TLS ───────────────────────────────────────────────────────
	var tlsCfg TLSConfig

	if behindNginx {
		fmt.Println()
		fmt.Println(dimStyle.Render("  TLS handled by nginx — skipping certificate setup"))
		tlsCfg = TLSConfig{Mode: "none"}
	} else {
		var tlsChoice string
		huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("TLS Certificate").
					Description("How to handle HTTPS").
					Options(
						huh.NewOption("Auto TLS — Let's Encrypt per subdomain (recommended)", "auto"),
						huh.NewOption("Cloudflare DNS — automatic wildcard cert", "cloudflare"),
						huh.NewOption("Manual — provide cert/key files", "manual"),
						huh.NewOption("No TLS — HTTP only", "none"),
					).
					Value(&tlsChoice),
			),
		).WithTheme(draculaTheme()).Run()

		switch tlsChoice {
		case "auto":
			tlsCfg = TLSConfig{Mode: "auto"}
			fmt.Println(successStyle.Render("  ✓") + " Auto TLS enabled (Let's Encrypt)")
			fmt.Println(dimStyle.Render("  Certificates issued automatically on first request."))
		case "cloudflare":
			tlsCfg = wizardCloudflare(domain)
		case "manual":
			tlsCfg = wizardManualCert()
		default:
			tlsCfg = TLSConfig{Mode: "none"}
		}
	}

	// ── 6. Server key ────────────────────────────────────────────────
	fmt.Println()
	fmt.Println(sectionStyle.Render("  Server Key"))
	fmt.Println()

	keyFile := "pipepie.key"
	kp, err := protocol.GenerateKeypair()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	keyContent := hex.EncodeToString(kp.Private) + "\n" + hex.EncodeToString(kp.Public) + "\n"
	os.WriteFile(keyFile, []byte(keyContent), 0600)
	pubKeyHex := hex.EncodeToString(kp.Public)

	fmt.Println(successStyle.Render("  ✓") + " Server key generated")
	fmt.Println("  " + dimStyle.Render("Public key") + "  " + keyStyle.Render(pubKeyHex))
	fmt.Println()
	fmt.Println(warnStyle.Render("  ⚠ Save this key! Clients need it to connect."))

	// ── 7. Save config ───────────────────────────────────────────────
	httpAddr := ":443"
	if behindNginx || tlsCfg.Mode == "none" {
		httpAddr = ":8080"
	}

	cfg := ServerConfig{
		Domain:     domain,
		TunnelAddr: ":9443",
		HTTPAddr:   httpAddr,
		DBPath:     "pipepie.db",
		KeyFile:    keyFile,
		TLS:        tlsCfg,
	}

	if dir := filepath.Dir(configPath); dir != "." && dir != "" {
		os.MkdirAll(dir, 0755)
	}
	out, _ := yaml.Marshal(cfg)
	if err := os.WriteFile(configPath, out, 0600); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	fmt.Println()
	fmt.Println(successStyle.Render("  ✓") + " Config saved to " + boldStyle.Render(configPath))

	// ── 8. Nginx config ──────────────────────────────────────────────
	if behindNginx && nginxInstalled {
		fmt.Println()
		fmt.Println(sectionStyle.Render("  Nginx Configuration"))
		fmt.Println()

		certPath, keyPath := "", ""
		if tlsCfg.Mode == "manual" {
			certPath, keyPath = tlsCfg.CertFile, tlsCfg.KeyFile
		}
		nginxConf := generateNginxConfig(domain, certPath, keyPath)

		nginxPath := findNginxConfigDir()
		if nginxPath != "" {
			if err := os.WriteFile(nginxPath, []byte(nginxConf), 0644); err == nil {
				fmt.Println(successStyle.Render("  ✓") + " Created " + dimStyle.Render(nginxPath))
				if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
					fmt.Println(errorStyle.Render("  ✗") + " nginx test failed: " + string(out))
				} else {
					exec.Command("systemctl", "reload", "nginx").Run()
					fmt.Println(successStyle.Render("  ✓") + " nginx reloaded")
				}
			}
		} else {
			fmt.Println(dimStyle.Render("  Add to nginx manually:"))
			fmt.Println()
			fmt.Println(dimStyle.Render(nginxConf))
		}
	}

	// ── 9. Systemd ───────────────────────────────────────────────────
	if runtime.GOOS == "linux" {
		fmt.Println()
		fmt.Println(sectionStyle.Render("  Systemd Service"))
		fmt.Println()

		piePath, _ := os.Executable()
		if piePath == "" {
			piePath = "/usr/local/bin/pie"
		}
		workDir, _ := os.Getwd()

		autoTLS := ""
		if tlsCfg.Mode == "auto" {
			autoTLS = " --auto-tls"
		}

		unit := fmt.Sprintf(`[Unit]
Description=pipepie tunnel server
After=network.target

[Service]
Type=simple
ExecStart=%s server --config %s%s
WorkingDirectory=%s
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
`, piePath, filepath.Join(workDir, configPath), autoTLS, workDir)

		servicePath := "/etc/systemd/system/pipepie.service"
		if err := os.WriteFile(servicePath, []byte(unit), 0644); err == nil {
			fmt.Println(successStyle.Render("  ✓") + " Created " + dimStyle.Render(servicePath))
			// Auto-enable and start
			exec.Command("systemctl", "daemon-reload").Run()
			if err := exec.Command("systemctl", "enable", "--now", "pipepie").Run(); err == nil {
				fmt.Println(successStyle.Render("  ✓") + " Server started and enabled on boot")
			} else {
				fmt.Println(dimStyle.Render("  Run: sudo systemctl enable --now pipepie"))
			}
		} else {
			fmt.Println(dimStyle.Render("  Could not write systemd unit (try running setup with sudo)"))
		}
	}

	// ── 10. Summary ──────────────────────────────────────────────────
	fmt.Println()
	fmt.Println("  ────────────────────────────────────")
	fmt.Println()
	fmt.Println(successStyle.Render("  ✓ Setup complete!"))
	fmt.Println()
	fmt.Println(dimStyle.Render("  Domain    ") + boldStyle.Render(domain))
	fmt.Println(dimStyle.Render("  Key       ") + keyStyle.Render(pubKeyHex[:24]+"..."))
	fmt.Println(dimStyle.Render("  Config    ") + configPath)
	fmt.Println()
	fmt.Println(boldStyle.Render("  Start server:"))
	fmt.Println(cyanStyle.Render("    pie server --config " + configPath))
	fmt.Println()
	fmt.Println(boldStyle.Render("  Connect from dev machine:"))
	fmt.Println(cyanStyle.Render(fmt.Sprintf("    pie login --server %s:9443 --key %s", domain, pubKeyHex)))
	fmt.Println(cyanStyle.Render("    pie connect 3000"))
	fmt.Println()

	return nil
}

// ── Cloudflare wizard ────────────────────────────────────────────────

func wizardCloudflare(domain string) TLSConfig {
	fmt.Println()
	fmt.Println(dimStyle.Render("  Get token: https://dash.cloudflare.com/profile/api-tokens"))
	fmt.Println(dimStyle.Render("  Permission: Zone → DNS → Edit"))
	fmt.Println()

	var token string
	huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Cloudflare API token").
				EchoMode(huh.EchoModePassword).
				Value(&token),
		),
	).WithTheme(draculaTheme()).Run()

	if token == "" {
		fmt.Println(errorStyle.Render("  Token required"))
		return TLSConfig{Mode: "none"}
	}

	cacheDir := "pipepie-certs"
	var certErr error
	spinner.New().
		Title(fmt.Sprintf("Requesting wildcard cert for *.%s...", domain)).
		Action(func() {
			certErr = obtainWildcardCert(domain, token, cacheDir)
		}).Run()

	if certErr != nil {
		fmt.Println(errorStyle.Render("  ✗ ") + certErr.Error())
		return TLSConfig{Mode: "cloudflare", CFToken: token, CacheDir: cacheDir}
	}

	fmt.Println(successStyle.Render("  ✓") + " Wildcard certificate issued!")
	return TLSConfig{Mode: "cloudflare", CFToken: token, CacheDir: cacheDir}
}

func obtainWildcardCert(domain, cfToken, cacheDir string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	storage := &certmagic.FileStorage{Path: cacheDir}
	cfg := certmagic.New(certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) {
			c := certmagic.Default
			return &c, nil
		},
	}), certmagic.Config{Storage: storage})

	issuer := certmagic.NewACMEIssuer(cfg, certmagic.ACMEIssuer{
		CA:                      certmagic.LetsEncryptProductionCA,
		Agreed:                  true,
		DisableHTTPChallenge:    true,
		DisableTLSALPNChallenge: true,
		DNS01Solver: &certmagic.DNS01Solver{
			DNSManager: certmagic.DNSManager{
				DNSProvider: &cloudflare.Provider{APIToken: cfToken},
			},
		},
	})
	cfg.Issuers = []certmagic.Issuer{issuer}

	return cfg.ManageSync(ctx, []string{"*." + domain, domain})
}

func wizardManualCert() TLSConfig {
	var certFile, keyFile string
	huh.NewForm(
		huh.NewGroup(
			huh.NewInput().Title("Certificate file (PEM)").Value(&certFile),
			huh.NewInput().Title("Private key file (PEM)").Value(&keyFile),
		),
	).WithTheme(draculaTheme()).Run()

	if _, err := tls.LoadX509KeyPair(certFile, keyFile); err != nil {
		fmt.Println(errorStyle.Render("  ✗ Invalid: ") + err.Error())
		return TLSConfig{Mode: "none"}
	}
	fmt.Println(successStyle.Render("  ✓") + " Certificate valid")
	return TLSConfig{Mode: "manual", CertFile: certFile, KeyFile: keyFile}
}

// ── Nginx ────────────────────────────────────────────────────────────

func isNginxInstalled() bool {
	_, err := exec.LookPath("nginx")
	return err == nil
}

func isPortBusy(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return true
	}
	ln.Close()
	return false
}

func findNginxConfigDir() string {
	for _, p := range []string{
		"/etc/nginx/sites-enabled/pipepie.conf",
		"/etc/nginx/conf.d/pipepie.conf",
	} {
		if fi, err := os.Stat(filepath.Dir(p)); err == nil && fi.IsDir() {
			return p
		}
	}
	return ""
}

func generateNginxConfig(domain, certFile, keyFile string) string {
	sslBlock := ""
	if certFile != "" && keyFile != "" {
		sslBlock = fmt.Sprintf("\n    ssl_certificate %s;\n    ssl_certificate_key %s;", certFile, keyFile)
	} else {
		sslBlock = strings.ReplaceAll(`
    # Get cert: sudo certbot certonly --nginx -d DOMAIN -d '*.DOMAIN'
    # ssl_certificate /etc/letsencrypt/live/DOMAIN/fullchain.pem;
    # ssl_certificate_key /etc/letsencrypt/live/DOMAIN/privkey.pem;`, "DOMAIN", domain)
	}

	return fmt.Sprintf(`server {
    listen 80;
    server_name %s *.%s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    server_name %s *.%s;
%s

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 86400;
    }
}
`, domain, domain, domain, domain, sslBlock)
}

// ── Helpers ──────────────────────────────────────────────────────────

func detectFirewall() string {
	if _, err := exec.LookPath("ufw"); err == nil {
		return "ufw"
	}
	if _, err := exec.LookPath("firewall-cmd"); err == nil {
		return "firewalld"
	}
	return "unknown"
}

func checkPortFromOutside(ip string, port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", ip, port), 2*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func checkDNS(host, expectedIP string) bool {
	lookup := strings.TrimPrefix(host, "*.")
	addrs, err := net.LookupHost(lookup)
	if err != nil {
		fmt.Println(dimStyle.Render(fmt.Sprintf("  ✗ %-35s not found", host)))
		return false
	}
	for _, a := range addrs {
		if a == expectedIP {
			fmt.Println(successStyle.Render("  ✓") + fmt.Sprintf(" %-35s → %s", host, a))
			return true
		}
	}
	fmt.Println(warnStyle.Render(fmt.Sprintf("  ✗ %-35s → %s (expected %s)", host, strings.Join(addrs, ","), expectedIP)))
	return false
}

func detectPublicIP() (string, error) {
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

func genToken() string {
	b := make([]byte, 12)
	rand.Read(b)
	return fmt.Sprintf("adm_%x", b)
}
