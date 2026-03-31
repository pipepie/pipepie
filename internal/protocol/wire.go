// Package protocol provides the pipepie wire protocol:
// length-prefixed Protobuf frames with zstd body compression.
//
// Wire format per frame:
//
//	[4 bytes: big-endian length] [N bytes: serialized pb.Frame]
package protocol

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

// CompressThreshold is the minimum body size before zstd kicks in.
const CompressThreshold = 1024

// MaxFrameSize caps a single frame to prevent OOM.
const MaxFrameSize = 16 << 20 // 16 MB

// Thread-safe zstd via sync.Pool.
var (
	encoderPool = sync.Pool{
		New: func() any {
			w, _ := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault))
			return w
		},
	}
	decoderPool = sync.Pool{
		New: func() any {
			r, _ := zstd.NewReader(nil)
			return r
		},
	}
)

// WriteFrame serializes a Frame and writes it with a 4-byte length prefix.
func WriteFrame(w io.Writer, f *pb.Frame) error {
	data, err := proto.Marshal(f)
	if err != nil {
		return fmt.Errorf("marshal frame: %w", err)
	}
	if len(data) > MaxFrameSize {
		return fmt.Errorf("frame too large: %d > %d", len(data), MaxFrameSize)
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// ReadFrame reads a length-prefixed Frame from the reader.
func ReadFrame(r io.Reader) (*pb.Frame, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	size := binary.BigEndian.Uint32(hdr[:])
	if size > uint32(MaxFrameSize) {
		return nil, fmt.Errorf("frame too large: %d", size)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, err
	}
	f := &pb.Frame{}
	if err := proto.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("unmarshal frame: %w", err)
	}
	return f, nil
}

// CompressBody zstd-compresses data if it exceeds CompressThreshold.
func CompressBody(data []byte) ([]byte, bool) {
	if len(data) < CompressThreshold {
		return data, false
	}
	enc := encoderPool.Get().(*zstd.Encoder)
	defer encoderPool.Put(enc)
	return enc.EncodeAll(data, nil), true
}

// DecompressBody decompresses zstd data.
func DecompressBody(data []byte, compressed bool) ([]byte, error) {
	if !compressed {
		return data, nil
	}
	dec := decoderPool.Get().(*zstd.Decoder)
	defer decoderPool.Put(dec)
	return dec.DecodeAll(data, nil)
}
