package server

import (
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/hashicorp/yamux"
)

// TCPProxy listens on a public TCP port and forwards connections
// to the client via yamux streams.
type TCPProxy struct {
	listenAddr string
	subdomain  string // which tunnel to forward to
	hub        *Hub
	log        *slog.Logger
}

// NewTCPProxy creates a TCP proxy for a specific tunnel.
func NewTCPProxy(listenAddr, subdomain string, hub *Hub, log *slog.Logger) *TCPProxy {
	return &TCPProxy{
		listenAddr: listenAddr,
		subdomain:  subdomain,
		hub:        hub,
		log:        log,
	}
}

// Run starts the TCP listener.
func (tp *TCPProxy) Run() error {
	ln, err := net.Listen("tcp", tp.listenAddr)
	if err != nil {
		return err
	}
	tp.log.Info("tcp proxy started", "addr", tp.listenAddr, "subdomain", tp.subdomain)
	for {
		conn, err := ln.Accept()
		if err != nil {
			tp.log.Error("tcp accept error", "err", err)
			continue
		}
		go tp.handleConn(conn)
	}
}

func (tp *TCPProxy) handleConn(incoming net.Conn) {
	defer incoming.Close()

	session := tp.hub.Get(tp.subdomain)
	if session == nil {
		tp.log.Debug("tcp: no client connected", "subdomain", tp.subdomain)
		return
	}

	// Get the yamux session from the tunnel session
	ns, ok := session.(*noiseSession)
	if !ok {
		tp.log.Error("tcp: session is not a noise session")
		return
	}

	// Open a new yamux stream for this TCP connection
	stream, err := ns.sess.Open()
	if err != nil {
		tp.log.Error("tcp: open yamux stream failed", "err", err)
		return
	}
	defer stream.Close()

	// Send a TCP proxy header so the client knows this is a TCP stream
	// Format: [1 byte: 0x01 = TCP proxy marker] [2 bytes: port] [rest: bidirectional copy]
	header := []byte{0x01} // TCP proxy marker
	if _, err := stream.Write(header); err != nil {
		return
	}

	tp.log.Debug("tcp: proxying connection", "subdomain", tp.subdomain, "remote", incoming.RemoteAddr())

	// Bidirectional copy
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		io.Copy(stream, incoming)
		// Signal write-close on yamux stream
		if ys, ok := stream.(*yamux.Stream); ok {
			ys.Close()
		}
	}()

	go func() {
		defer wg.Done()
		io.Copy(incoming, stream)
		incoming.Close()
	}()

	wg.Wait()
}

// TCPProxyClient handles incoming TCP proxy streams on the client side.
// It reads the 0x01 marker and proxies to the local target.
func TCPProxyClient(stream net.Conn, localAddr string) {
	defer stream.Close()

	// Read marker byte
	marker := make([]byte, 1)
	if _, err := io.ReadFull(stream, marker); err != nil {
		return
	}
	if marker[0] != 0x01 {
		return // not a TCP proxy stream
	}

	// Connect to local target
	local, err := net.Dial("tcp", localAddr)
	if err != nil {
		return
	}
	defer local.Close()

	// Bidirectional copy
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(local, stream) }()
	go func() { defer wg.Done(); io.Copy(stream, local) }()
	wg.Wait()
}

// unused but reserved for future port encoding
