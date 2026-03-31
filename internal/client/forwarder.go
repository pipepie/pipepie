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
		// Local server not running — return friendly error page
		return &pb.HttpResponse{
			Id:     req.Id,
			Status: 502,
			Headers: map[string]string{
				"Content-Type": "text/html; charset=utf-8",
			},
			Body: []byte(errorPage(f.target, err)),
		}, nil
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

func errorPage(target string, err error) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<title>pipepie — tunnel active</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body {
    background: #0f172a; color: #e2e8f0; font-family: -apple-system, system-ui, sans-serif;
    display: flex; align-items: center; justify-content: center; min-height: 100vh;
  }
  .card {
    max-width: 480px; text-align: center; padding: 48px 32px;
  }
  .logo { font-size: 32px; font-weight: 700; color: #bd93f9; margin-bottom: 8px; }
  .subtitle { color: #6272a4; font-size: 14px; margin-bottom: 32px; }
  .status {
    background: #1e293b; border: 1px solid #334155; border-radius: 12px;
    padding: 24px; margin-bottom: 24px;
  }
  .status h2 { color: #f1fa8c; font-size: 18px; margin-bottom: 8px; }
  .status p { color: #94a3b8; font-size: 14px; line-height: 1.6; }
  .target {
    font-family: 'SF Mono', monospace; color: #8be9fd;
    background: #0f172a; padding: 2px 8px; border-radius: 4px;
  }
  .hint { color: #6272a4; font-size: 13px; }
  .hint code {
    background: #1e293b; padding: 2px 8px; border-radius: 4px;
    color: #50fa7b; font-family: 'SF Mono', monospace;
  }
</style>
</head>
<body>
<div class="card">
  <div class="logo">pipepie</div>
  <div class="subtitle">encrypted tunnel</div>
  <div class="status">
    <h2>Tunnel is active</h2>
    <p>But nothing is running on <span class="target">%s</span></p>
  </div>
  <p class="hint">Start your local server and refresh this page.</p>
</div>
</body>
</html>`, target)
}
