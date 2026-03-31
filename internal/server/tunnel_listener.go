package server

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/protocol"
	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
	"github.com/Seinarukiro2/pipepie/internal/store"
	"github.com/flynn/noise"
	"github.com/hashicorp/yamux"
)

func generateSubdomain() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// TunnelListener accepts TCP connections from pie clients.
// Auth is handled at the Noise NK level — only clients who know
// the server's public key can complete the handshake.
type TunnelListener struct {
	addr      string
	domain    string
	serverKey noise.DHKey
	store     *store.Store
	hub       *Hub
	dashAuth  *DashboardAuth
	log       *slog.Logger
}

// NewTunnelListener creates a tunnel listener.
func NewTunnelListener(addr, domain string, key noise.DHKey, st *store.Store, hub *Hub, da *DashboardAuth, log *slog.Logger) *TunnelListener {
	return &TunnelListener{addr: addr, domain: domain, serverKey: key, store: st, hub: hub, dashAuth: da, log: log}
}

// Run starts the TCP listener. Blocks.
func (tl *TunnelListener) Run() error {
	ln, err := net.Listen("tcp", tl.addr)
	if err != nil {
		return err
	}
	tl.log.Info("tunnel listener started", "addr", tl.addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			tl.log.Error("accept error", "err", err)
			continue
		}
		go tl.handleConn(conn)
	}
}

func (tl *TunnelListener) handleConn(raw net.Conn) {
	defer raw.Close()

	// 1. Noise NK handshake — this IS the auth.
	//    Only clients who know our public key can complete it.
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	encrypted, err := protocol.ServerHandshake(raw, tl.serverKey)
	if err != nil {
		tl.log.Debug("handshake rejected", "remote", raw.RemoteAddr(), "err", err)
		return
	}
	raw.SetDeadline(time.Time{})

	// 2. yamux session
	sess, err := yamux.Server(encrypted, yamux.DefaultConfig())
	if err != nil {
		tl.log.Error("yamux failed", "err", err)
		return
	}
	defer sess.Close()

	// 3. Accept control stream
	ctrl, err := sess.Accept()
	if err != nil {
		return
	}

	// 4. Read hello frame (subdomain request, no token needed)
	frame, err := protocol.ReadFrame(ctrl)
	if err != nil {
		return
	}
	auth := frame.GetAuth()
	if auth == nil {
		protocol.WriteFrame(ctrl, &pb.Frame{
			Payload: &pb.Frame_AuthError{AuthError: &pb.AuthError{Message: "expected hello"}},
		})
		return
	}

	// 5. Assign subdomain — requested or auto-generated
	subdomain := auth.Subdomain
	if subdomain == "" {
		subdomain = generateSubdomain()
	}

	// Ensure tunnel exists in store
	tunnel, err := tl.store.TunnelBySubdomain(subdomain)
	if err != nil {
		// Create it — no auth needed, Noise NK is the auth
		tunnel, err = tl.store.TunnelCreate(subdomain, "")
		if err != nil {
			// Subdomain taken by another session — append random suffix
			subdomain = subdomain + "-" + generateSubdomain()[:4]
			tunnel, err = tl.store.TunnelCreate(subdomain, "")
			if err != nil {
				protocol.WriteFrame(ctrl, &pb.Frame{
					Payload: &pb.Frame_AuthError{AuthError: &pb.AuthError{Message: "failed to create tunnel"}},
				})
				return
			}
		}
	}

	tl.store.TunnelTouch(subdomain)

	// 6. Send OK with assigned subdomain
	protocol.WriteFrame(ctrl, &pb.Frame{
		Payload: &pb.Frame_AuthOk{AuthOk: &pb.AuthOK{
			Subdomain: subdomain,
			PublicUrl: "https://" + subdomain + "." + tl.domain,
		}},
	})

	// 7. Register session
	session := &noiseSession{
		subdomain:   subdomain,
		tunnelID:    tunnel.ID,
		remoteAddr:  raw.RemoteAddr().String(),
		connectedAt: time.Now(),
		sess:        sess,
		ctrl:        ctrl,
		pending:     &sync.Map{},
	}
	tl.hub.Register(session)
	defer tl.hub.Unregister(session)

	tl.log.Info("client connected", "subdomain", subdomain, "remote", raw.RemoteAddr())

	// 8. Read loop
	for {
		frame, err := protocol.ReadFrame(ctrl)
		if err != nil {
			if err != io.EOF {
				tl.log.Debug("tunnel read error", "subdomain", subdomain, "err", err)
			}
			return
		}
		switch p := frame.Payload.(type) {
		case *pb.Frame_Response:
			if ch, ok := session.pending.Load(p.Response.Id); ok {
				ch.(chan *pb.HttpResponse) <- p.Response
			}
		case *pb.Frame_ForwardError:
			if ch, ok := session.pending.Load(p.ForwardError.Id); ok {
				ch.(chan *pb.HttpResponse) <- &pb.HttpResponse{
					Id: p.ForwardError.Id, Status: 502,
				}
			}
		case *pb.Frame_DashTokenReq:
			token := tl.dashAuth.GenerateToken()
			url := fmt.Sprintf("https://%s/ui/?t=%s", tl.domain, token)
			session.ctrlMu.Lock()
			protocol.WriteFrame(ctrl, &pb.Frame{
				Payload: &pb.Frame_DashTokenResp{DashTokenResp: &pb.DashboardTokenResp{
					Token: token,
					Url:   url,
				}},
			})
			session.ctrlMu.Unlock()
			tl.log.Info("dashboard token issued", "subdomain", subdomain)
		case *pb.Frame_Pong:
			// keepalive
		}
	}
}

// noiseSession implements TunnelSession.
type noiseSession struct {
	subdomain   string
	tunnelID    string
	remoteAddr  string
	connectedAt time.Time
	sess        *yamux.Session
	ctrl        net.Conn
	ctrlMu      sync.Mutex // protects concurrent writes to ctrl
	pending     *sync.Map
}

func (s *noiseSession) Subdomain() string      { return s.subdomain }
func (s *noiseSession) Protocol() string        { return "noise-nk+yamux+protobuf+zstd" }
func (s *noiseSession) RemoteAddr() string      { return s.remoteAddr }
func (s *noiseSession) ConnectedAt() time.Time  { return s.connectedAt }
func (s *noiseSession) Close() error            { return s.sess.Close() }

func (s *noiseSession) SendRequest(req *pb.HttpRequest, timeout time.Duration) (*pb.HttpResponse, error) {
	ch := make(chan *pb.HttpResponse, 1)
	s.pending.Store(req.Id, ch)
	defer s.pending.Delete(req.Id)

	s.ctrlMu.Lock()
	err := protocol.WriteFrame(s.ctrl, &pb.Frame{
		Payload: &pb.Frame_Request{Request: req},
	})
	s.ctrlMu.Unlock()
	if err != nil {
		return nil, err
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-time.After(timeout):
		return nil, nil
	}
}
