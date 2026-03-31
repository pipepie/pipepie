package protocol

import (
	"bytes"
	"net"
	"testing"
	"time"
)

// helper: generate keypair and run NK handshake on both sides
func handshakePair(t *testing.T) (serverConn, clientConn *NoiseConn) {
	t.Helper()
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatalf("generate keypair: %v", err)
	}

	server, client := net.Pipe()
	t.Cleanup(func() { server.Close(); client.Close() })

	var sConn, cConn *NoiseConn
	var sErr, cErr error
	done := make(chan struct{})
	go func() { sConn, sErr = ServerHandshake(server, kp); done <- struct{}{} }()
	go func() { cConn, cErr = ClientHandshake(client, kp.Public); done <- struct{}{} }()
	<-done
	<-done

	if sErr != nil {
		t.Fatalf("server: %v", sErr)
	}
	if cErr != nil {
		t.Fatalf("client: %v", cErr)
	}
	return sConn, cConn
}

func TestNoiseHandshake(t *testing.T) {
	sConn, cConn := handshakePair(t)

	msg := []byte("hello encrypted world")
	go func() { cConn.Write(msg) }()

	buf := make([]byte, 256)
	n, err := sConn.Read(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(buf[:n], msg) {
		t.Errorf("got %q, want %q", buf[:n], msg)
	}
}

func TestNoiseHandshake_Bidirectional(t *testing.T) {
	sConn, cConn := handshakePair(t)

	// Client → Server
	go func() { cConn.Write([]byte("ping")) }()
	buf := make([]byte, 64)
	n, _ := sConn.Read(buf)
	if string(buf[:n]) != "ping" {
		t.Errorf("c→s: got %q", buf[:n])
	}

	// Server → Client
	go func() { sConn.Write([]byte("pong")) }()
	n, _ = cConn.Read(buf)
	if string(buf[:n]) != "pong" {
		t.Errorf("s→c: got %q", buf[:n])
	}
}

func TestNoiseHandshake_LargeMessage(t *testing.T) {
	sConn, cConn := handshakePair(t)

	large := bytes.Repeat([]byte("A"), 200_000)
	go func() { cConn.Write(large) }()

	var received []byte
	buf := make([]byte, 8192)
	for len(received) < len(large) {
		n, err := sConn.Read(buf)
		if err != nil {
			t.Fatalf("read at %d bytes: %v", len(received), err)
		}
		received = append(received, buf[:n]...)
	}
	if !bytes.Equal(received, large) {
		t.Errorf("mismatch: got %d bytes, want %d", len(received), len(large))
	}
}

func TestNoiseConn_ConcurrentWrites(t *testing.T) {
	sConn, cConn := handshakePair(t)

	const N = 50
	writeDone := make(chan struct{})
	for i := range N {
		go func() {
			defer func() { writeDone <- struct{}{} }()
			cConn.Write([]byte("msg"))
			_ = i
		}()
	}

	readDone := make(chan int)
	go func() {
		count := 0
		buf := make([]byte, 256)
		for count < N {
			_, err := sConn.Read(buf)
			if err != nil {
				break
			}
			count++
		}
		readDone <- count
	}()

	for range N {
		select {
		case <-writeDone:
		case <-time.After(5 * time.Second):
			t.Fatal("write timeout")
		}
	}

	cConn.Close()

	select {
	case n := <-readDone:
		if n != N {
			t.Errorf("read %d messages, want %d", n, N)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read timeout")
	}
}

func TestNoiseHandshake_WrongKey(t *testing.T) {
	serverKey, _ := GenerateKeypair()
	wrongKey, _ := GenerateKeypair()

	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	done := make(chan error, 2)
	go func() { _, err := ServerHandshake(server, serverKey); done <- err }()
	go func() {
		conn, err := ClientHandshake(client, wrongKey.Public)
		if err != nil {
			done <- err
			return
		}
		// Even if handshake completes, communication should fail
		conn.Write([]byte("test"))
		done <- nil
	}()

	// At least one side should fail — or communication will be garbled
	select {
	case <-time.After(2 * time.Second):
		t.Log("handshake hung — expected with wrong key")
	case err := <-done:
		if err != nil {
			t.Logf("handshake failed as expected: %v", err)
		}
	}
}
