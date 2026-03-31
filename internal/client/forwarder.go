package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Seinarukiro2/pipepie/internal/protocol/pb"
)

// Forwarder forwards webhook requests to the local target.
type Forwarder struct {
	target string
	http   *http.Client
}

// NewForwarder creates a forwarder targeting the given URL.
func NewForwarder(target string) *Forwarder {
	return &Forwarder{
		target: strings.TrimRight(target, "/"),
		http:   &http.Client{Timeout: 30 * time.Second},
	}
}

// Forward sends a protobuf request to the local target and returns the response.
func (f *Forwarder) Forward(req *pb.HttpRequest, body []byte) (*pb.HttpResponse, error) {
	url := f.target + req.Path
	if req.Query != "" {
		url += "?" + req.Query
	}

	var bodyReader io.Reader
	if len(body) > 0 {
		bodyReader = bytes.NewReader(body)
	}

	httpReq, err := http.NewRequest(req.Method, url, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	skip := map[string]bool{"Host": true, "Connection": true, "Transfer-Encoding": true}
	for k, v := range req.Headers {
		if !skip[k] {
			httpReq.Header.Set(k, v)
		}
	}
	httpReq.Header.Set("X-Pipepie-Request-ID", req.Id)
	if req.ReplayOf != "" {
		httpReq.Header.Set("X-Pipepie-Replay-Of", req.ReplayOf)
	}

	resp, err := f.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("forward: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	headers := make(map[string]string, len(resp.Header))
	for k, v := range resp.Header {
		headers[k] = strings.Join(v, ", ")
	}

	return &pb.HttpResponse{
		Id:      req.Id,
		Status:  int32(resp.StatusCode),
		Headers: headers,
		Body:    respBody,
	}, nil
}
