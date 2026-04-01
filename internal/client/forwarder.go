package client

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/pipepie/pipepie/internal/protocol/pb"
)

// Forwarder forwards webhook requests to the local target.
type Forwarder struct {
	target     string
	http       *http.Client
	streamHttp *http.Client // no timeout for SSE/streaming
}

// NewForwarder creates a forwarder targeting the given URL.
func NewForwarder(target string) *Forwarder {
	return &Forwarder{
		target:     strings.TrimRight(target, "/"),
		http:       &http.Client{Timeout: 30 * time.Second},
		streamHttp: &http.Client{Timeout: 0}, // no timeout for streams
	}
}

func isStreamingRequest(headers map[string]string) bool {
	accept := strings.ToLower(headers["Accept"])
	return strings.Contains(accept, "text/event-stream") || strings.Contains(accept, "application/x-ndjson")
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

	// Use a client without timeout for SSE/streaming responses
	httpClient := f.http
	if isStreamingRequest(req.Headers) {
		httpClient = f.streamHttp
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		// Local server not running — return friendly error page
		return &pb.HttpResponse{
			Id:     req.Id,
			Status: 502,
			Headers: map[string]string{
				"Content-Type": "text/html; charset=utf-8",
			},
			Body: []byte(errorPage(f.target)),
		}, nil
	}
	defer resp.Body.Close()

	// Check if response is SSE/streaming — read with a reasonable limit
	// but don't hang forever on streams
	maxBody := int64(10 << 20) // 10MB
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") || strings.Contains(ct, "application/x-ndjson") {
		// For SSE: read available data with timeout, don't wait for EOF
		maxBody = 1 << 20 // 1MB for streaming responses
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil && err != io.EOF {
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

func errorPage(target string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>pipepie — tunnel active</title>
<link href="https://fonts.googleapis.com/css2?family=VT323&display=swap" rel="stylesheet">
<style>
*{margin:0;padding:0;box-sizing:border-box}
body{background:#282a36;color:#f8f8f2;font-family:'VT323',monospace;min-height:100vh;display:flex;align-items:center;justify-content:center;overflow:hidden}
body::before{content:'';position:fixed;inset:0;background:repeating-linear-gradient(0deg,transparent,transparent 2px,rgba(0,0,0,.08) 2px,rgba(0,0,0,.08) 4px);pointer-events:none;z-index:10}
body::after{content:'';position:fixed;inset:0;background:radial-gradient(ellipse at center,transparent 50%%,rgba(0,0,0,.4));pointer-events:none;z-index:10}
@keyframes flicker{0%%,100%%{opacity:1}92%%{opacity:1}93%%{opacity:.8}94%%{opacity:1}}
@keyframes blink{0%%,100%%{opacity:1}50%%{opacity:0}}
@keyframes glow{0%%,100%%{text-shadow:0 0 10px rgba(189,147,249,.3)}50%%{text-shadow:0 0 20px rgba(189,147,249,.6),0 0 40px rgba(189,147,249,.2)}}
.screen{animation:flicker 4s infinite;max-width:600px;padding:40px}
.pie{color:#bd93f9;font-size:20px;line-height:1.1;margin-bottom:16px;white-space:pre}
.title{font-size:48px;color:#bd93f9;animation:glow 3s ease-in-out infinite;margin-bottom:4px}
.sub{color:#6272a4;font-size:24px;margin-bottom:32px}
.box{border:1px solid #44475a;padding:20px;margin-bottom:20px;position:relative}
.box::before{content:'[ STATUS ]';position:absolute;top:-10px;left:12px;background:#282a36;padding:0 8px;color:#6272a4;font-size:18px}
.line{font-size:22px;margin:6px 0}
.ok{color:#50fa7b}.warn{color:#f1fa8c}.target{color:#8be9fd}.pink{color:#ff79c6}
.cursor{display:inline-block;animation:blink 1s step-end infinite;color:#50fa7b}
.prompt{color:#6272a4;font-size:20px;margin-top:24px}
.prompt span{color:#50fa7b}
</style>
</head>
<body>
<div class="screen">
<pre class="pie">    ╱◣
   ╱  ◣
  ╱ ◈  ◣
 ╱──────◣
 ‾‾‾‾‾‾‾‾</pre>
<div class="title">pipepie</div>
<div class="sub">encrypted tunnel</div>
<div class="box">
<div class="line ok">► tunnel is active</div>
<div class="line warn">► no service detected on <span class="target">%s</span></div>
<div class="line pink">► start your server and refresh</div>
</div>
<div class="prompt">$ pie connect 3000 <span class="cursor">█</span></div>
</div>
</body>
</html>`, target)
}
