// Package protocol — Noise Protocol encryption for pipepie.
//
// Uses Noise_NK handshake:
//   - Server has a static keypair (generated at 'pie setup')
//   - Client knows the server's public key (saved at 'pie login')
//   - Pattern: → e, es  ← e, ee  (2 messages)
//   - All traffic encrypted with ChaChaPoly + BLAKE2b
package protocol

import (
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/flynn/noise"
)

var cipherSuite = noise.NewCipherSuite(
	noise.DH25519,
	noise.CipherChaChaPoly,
	noise.HashBLAKE2b,
)

// GenerateKeypair creates a new Noise static keypair.
func GenerateKeypair() (noise.DHKey, error) {
	return cipherSuite.GenerateKeypair(nil)
}

// NoiseConn wraps a net.Conn with Noise encryption. Thread-safe.
type NoiseConn struct {
	raw     net.Conn
	send    *noise.CipherState
	recv    *noise.CipherState
	readMu  sync.Mutex
	readBuf []byte
	writeMu sync.Mutex
}

// ServerHandshake performs Noise NK as responder.
func ServerHandshake(conn net.Conn, staticKey noise.DHKey) (*NoiseConn, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:  cipherSuite,
		Pattern:      noise.HandshakeNK,
		Initiator:    false,
		StaticKeypair: staticKey,
		Prologue:     []byte("pipepie/1"),
	})
	if err != nil {
		return nil, fmt.Errorf("noise init: %w", err)
	}

	// Message 1: read → e, es
	msg1, err := readRaw(conn)
	if err != nil {
		return nil, fmt.Errorf("read msg1: %w", err)
	}
	if _, _, _, err = hs.ReadMessage(nil, msg1); err != nil {
		return nil, fmt.Errorf("process msg1: %w", err)
	}

	// Message 2: write ← e, ee
	msg2, cs0, cs1, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("write msg2: %w", err)
	}
	if err := writeRaw(conn, msg2); err != nil {
		return nil, err
	}
	if cs0 == nil || cs1 == nil {
		return nil, fmt.Errorf("handshake incomplete")
	}
	return &NoiseConn{raw: conn, send: cs1, recv: cs0}, nil
}

// ClientHandshake performs Noise NK as initiator.
func ClientHandshake(conn net.Conn, serverPubKey []byte) (*NoiseConn, error) {
	hs, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: cipherSuite,
		Pattern:     noise.HandshakeNK,
		Initiator:   true,
		PeerStatic:  serverPubKey,
		Prologue:    []byte("pipepie/1"),
	})
	if err != nil {
		return nil, fmt.Errorf("noise init: %w", err)
	}

	// Message 1: write → e, es
	msg1, _, _, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("write msg1: %w", err)
	}
	if err := writeRaw(conn, msg1); err != nil {
		return nil, err
	}

	// Message 2: read ← e, ee
	msg2, err := readRaw(conn)
	if err != nil {
		return nil, fmt.Errorf("read msg2: %w", err)
	}
	_, cs0, cs1, err := hs.ReadMessage(nil, msg2)
	if err != nil {
		return nil, fmt.Errorf("process msg2: %w", err)
	}
	if cs0 == nil || cs1 == nil {
		return nil, fmt.Errorf("handshake incomplete")
	}
	return &NoiseConn{raw: conn, send: cs0, recv: cs1}, nil
}

// Read decrypts data. Thread-safe.
func (c *NoiseConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}
	ct, err := readRaw(c.raw)
	if err != nil {
		return 0, err
	}
	pt, err := c.recv.Decrypt(nil, nil, ct)
	if err != nil {
		return 0, fmt.Errorf("decrypt: %w", err)
	}
	n := copy(p, pt)
	if n < len(pt) {
		c.readBuf = append(c.readBuf[:0], pt[n:]...)
	}
	return n, nil
}

const maxChunk = 65535 - 64

// Write encrypts data. Thread-safe. Chunks large writes.
func (c *NoiseConn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	written := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > maxChunk {
			chunk = p[:maxChunk]
		}
		ct, err := c.send.Encrypt(nil, nil, chunk)
		if err != nil {
			return written, fmt.Errorf("encrypt: %w", err)
		}
		if err := writeRaw(c.raw, ct); err != nil {
			return written, err
		}
		written += len(chunk)
		p = p[len(chunk):]
	}
	return written, nil
}

func (c *NoiseConn) Close() error { return c.raw.Close() }

func writeRaw(w io.Writer, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("message too large: %d", len(data))
	}
	hdr := [2]byte{byte(len(data) >> 8), byte(len(data))}
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func readRaw(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := int(hdr[0])<<8 | int(hdr[1])
	data := make([]byte, size)
	_, err := io.ReadFull(r, data)
	return data, err
}
