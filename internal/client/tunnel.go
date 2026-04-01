package client

import (
	"context"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pipepie/pipepie/internal/protocol"
	"github.com/pipepie/pipepie/internal/protocol/pb"
	"github.com/hashicorp/yamux"
)

// Config holds client configuration.
type Config struct {
	ServerAddr   string // TCP address, e.g. "hook.example.com:9443"
	ServerPubKey []byte // server's Noise NK public key (32 bytes)
	Subdomain    string
	Forward      string // e.g. "http://localhost:3000"
	TCPForward   string // for TCP proxy mode, e.g. "localhost:5432"
	Auth         string // password for public URL protection
}

// Client connects to pipepie server via Noise+yamux+Protobuf.
type Client struct {
	cfg     Config
	display *Display
	fwd     *Forwarder
}

// New creates a new client.
func New(cfg Config) *Client {
	return &Client{
		cfg:     cfg,
		display: NewDisplay(),
		fwd:     NewForwarder(cfg.Forward),
	}
}

// AssignedSubdomain returns the subdomain assigned by the server (after connect).
func (c *Client) AssignedSubdomain() string {
	return c.cfg.Subdomain
}

// Run connects and forwards webhooks. Auto-reconnects on failure.
func (c *Client) Run(ctx context.Context) error {
	attempt := 0
	for {
		err := c.connect(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		attempt++
		delay := backoff(attempt)
		c.display.Reconnecting(attempt, delay, err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
}

func (c *Client) connect(ctx context.Context) error {
	// 1. TCP connect
	conn, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", c.cfg.ServerAddr)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	// 2. Noise NK handshake — verifies server identity via public key
	encrypted, err := protocol.ClientHandshake(conn, c.cfg.ServerPubKey)
	if err != nil {
		return fmt.Errorf("noise: %w", err)
	}

	// 3. yamux session
	sess, err := yamux.Client(encrypted, yamux.DefaultConfig())
	if err != nil {
		return fmt.Errorf("yamux: %w", err)
	}
	defer sess.Close()

	// 4. Open control stream
	ctrl, err := sess.Open()
	if err != nil {
		return fmt.Errorf("open stream: %w", err)
	}

	// 5. Hello — request subdomain (no token needed, Noise NK is the auth)
	if err := protocol.WriteFrame(ctrl, &pb.Frame{
		Payload: &pb.Frame_Auth{Auth: &pb.Auth{
			Subdomain: c.cfg.Subdomain,
			Version:   "0.1.0",
		}},
	}); err != nil {
		return fmt.Errorf("send auth: %w", err)
	}

	// 6. Auth response
	frame, err := protocol.ReadFrame(ctrl)
	if err != nil {
		return fmt.Errorf("read auth: %w", err)
	}
	switch p := frame.Payload.(type) {
	case *pb.Frame_AuthOk:
		c.display.Connected(p.AuthOk.PublicUrl, c.cfg.Forward)
		// Remember assigned subdomain for reconnects
		if p.AuthOk.Subdomain != "" && c.cfg.Subdomain == "" {
			c.cfg.Subdomain = p.AuthOk.Subdomain
		}
	case *pb.Frame_AuthError:
		return fmt.Errorf("auth rejected: %s", p.AuthError.Message)
	default:
		return fmt.Errorf("unexpected response")
	}

	// 7. Accept TCP proxy streams from server (if TCPForward is set)
	if c.cfg.TCPForward != "" {
		go c.acceptTCPStreams(sess)
	}

	// Write mutex for control stream — multiple goroutines write responses
	var ctrlMu sync.Mutex
	writeFrame := func(f *pb.Frame) {
		ctrlMu.Lock()
		defer ctrlMu.Unlock()
		protocol.WriteFrame(ctrl, f)
	}

	// 8. Main loop (control stream: protobuf requests + keepalive)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := protocol.ReadFrame(ctrl)
		if err != nil {
			if err != io.EOF {
				return fmt.Errorf("read: %w", err)
			}
			return err
		}
		switch p := frame.Payload.(type) {
		case *pb.Frame_Request:
			go c.handleRequestSafe(writeFrame, p.Request)
		case *pb.Frame_Ping:
			writeFrame(&pb.Frame{Payload: &pb.Frame_Pong{Pong: &pb.Pong{}}})
		}
	}
}

func (c *Client) handleRequestSafe(write func(*pb.Frame), req *pb.HttpRequest) {
	start := time.Now()

	// Auth check — if --auth is set, verify before forwarding
	if c.cfg.Auth != "" && !c.checkAuth(req) {
		resp := &pb.HttpResponse{
			Id:     req.Id,
			Status: 401,
			Headers: map[string]string{
				"Content-Type":     "text/plain",
				"WWW-Authenticate": "Bearer",
			},
			Body: []byte("unauthorized"),
		}
		resp.DurationMs = time.Since(start).Milliseconds()
		c.display.Request(req.Method, req.Path, 401, time.Since(start), false)
		write(&pb.Frame{Payload: &pb.Frame_Response{Response: resp}})
		return
	}

	body, err := protocol.DecompressBody(req.Body, req.Compressed)
	if err != nil {
		c.display.Error(req.Method, req.Path, err)
		write(&pb.Frame{Payload: &pb.Frame_ForwardError{ForwardError: &pb.ForwardError{Id: req.Id, Message: err.Error()}}})
		return
	}

	resp, err := c.fwd.Forward(req, body)
	duration := time.Since(start)

	if err != nil {
		c.display.Error(req.Method, req.Path, err)
		write(&pb.Frame{Payload: &pb.Frame_ForwardError{ForwardError: &pb.ForwardError{Id: req.Id, Message: err.Error()}}})
		return
	}

	compBody, compressed := protocol.CompressBody(resp.Body)
	resp.Body = compBody
	resp.Compressed = compressed
	resp.DurationMs = duration.Milliseconds()

	c.display.RequestWithSize(req.Method, req.Path, int(resp.Status), duration, len(body), req.ReplayOf != "")
	write(&pb.Frame{Payload: &pb.Frame_Response{Response: resp}})
}

func (c *Client) checkAuth(req *pb.HttpRequest) bool {
	pw := c.cfg.Auth

	// Check Authorization: Bearer <password>
	if auth, ok := req.Headers["Authorization"]; ok {
		if auth == "Bearer "+pw || auth == "bearer "+pw {
			return true
		}
	}

	// Check ?auth=<password> in query string
	if strings.Contains(req.Query, "auth="+pw) {
		return true
	}

	// Check cookie
	if cookie, ok := req.Headers["Cookie"]; ok {
		if strings.Contains(cookie, "pipepie_auth="+pw) {
			return true
		}
	}

	return false
}

func (c *Client) acceptTCPStreams(sess *yamux.Session) {
	for {
		stream, err := sess.Accept()
		if err != nil {
			return // session closed
		}
		go func() {
			// Peek first byte — if 0x01, it's a TCP proxy stream
			marker := make([]byte, 1)
			if _, err := io.ReadFull(stream, marker); err != nil {
				stream.Close()
				return
			}
			if marker[0] == 0x01 {
				c.display.TCPConnection(c.cfg.TCPForward)
				proxyTCP(stream, c.cfg.TCPForward)
			} else {
				stream.Close()
			}
		}()
	}
}

func proxyTCP(stream net.Conn, localAddr string) {
	defer stream.Close()
	local, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
	if err != nil {
		return
	}
	defer local.Close()
	done := make(chan struct{})
	go func() { io.Copy(local, stream); done <- struct{}{} }()
	go func() { io.Copy(stream, local); done <- struct{}{} }()
	<-done
}

func backoff(attempt int) time.Duration {
	base := math.Min(float64(attempt)*float64(attempt), 30)
	jitter := rand.Float64() * base * 0.3
	return time.Duration(base+jitter) * time.Second
}
