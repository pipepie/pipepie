package protocol

import (
	"bytes"
	"testing"

	"github.com/pipepie/pipepie/internal/protocol/pb"
)

func TestWriteReadFrame(t *testing.T) {
	var buf bytes.Buffer

	orig := &pb.Frame{
		Payload: &pb.Frame_Auth{Auth: &pb.Auth{
			Token:     "tok_test",
			Subdomain: "myapp",
			Version:   "0.1.0",
		}},
	}

	if err := WriteFrame(&buf, orig); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	auth := got.GetAuth()
	if auth == nil {
		t.Fatal("expected Auth payload")
	}
	if auth.Token != "tok_test" {
		t.Errorf("token = %q, want %q", auth.Token, "tok_test")
	}
	if auth.Subdomain != "myapp" {
		t.Errorf("subdomain = %q, want %q", auth.Subdomain, "myapp")
	}
}

func TestWriteReadFrame_Request(t *testing.T) {
	var buf bytes.Buffer

	body := []byte(`{"event":"test"}`)
	compBody, compressed := CompressBody(body)

	orig := &pb.Frame{
		Payload: &pb.Frame_Request{Request: &pb.HttpRequest{
			Id:         "req-1",
			Method:     "POST",
			Path:       "/webhook",
			Headers:    map[string]string{"Content-Type": "application/json"},
			Body:       compBody,
			Compressed: compressed,
		}},
	}

	if err := WriteFrame(&buf, orig); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	got, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	req := got.GetRequest()
	if req == nil {
		t.Fatal("expected Request payload")
	}
	if req.Method != "POST" {
		t.Errorf("method = %q, want POST", req.Method)
	}

	gotBody, err := DecompressBody(req.Body, req.Compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(gotBody, body) {
		t.Errorf("body = %q, want %q", gotBody, body)
	}
}

func TestWriteReadFrame_MultipleFrames(t *testing.T) {
	var buf bytes.Buffer

	for i := range 100 {
		f := &pb.Frame{
			Payload: &pb.Frame_Ping{Ping: &pb.Ping{}},
		}
		_ = i
		if err := WriteFrame(&buf, f); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	for i := range 100 {
		f, err := ReadFrame(&buf)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if f.GetPing() == nil {
			t.Fatalf("frame %d: expected Ping", i)
		}
	}
}

func TestReadFrame_TooLarge(t *testing.T) {
	var buf bytes.Buffer
	// Write a fake header claiming 20MB
	hdr := []byte{0x01, 0x40, 0x00, 0x00} // ~20MB
	buf.Write(hdr)
	buf.Write(make([]byte, 100))

	_, err := ReadFrame(&buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
}

func TestCompressBody_SmallSkipped(t *testing.T) {
	small := []byte("hello")
	out, compressed := CompressBody(small)
	if compressed {
		t.Error("small body should not be compressed")
	}
	if !bytes.Equal(out, small) {
		t.Error("small body should be returned as-is")
	}
}

func TestCompressBody_LargeCompressed(t *testing.T) {
	large := bytes.Repeat([]byte("x"), 2048)
	out, compressed := CompressBody(large)
	if !compressed {
		t.Error("large body should be compressed")
	}
	if len(out) >= len(large) {
		t.Error("compressed should be smaller than original for repetitive data")
	}

	decompressed, err := DecompressBody(out, true)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(decompressed, large) {
		t.Error("round-trip failed")
	}
}

func TestDecompressBody_NotCompressed(t *testing.T) {
	data := []byte("raw")
	out, err := DecompressBody(data, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Error("passthrough failed")
	}
}
