// Package store provides SQLite-backed persistence for tunnels and requests.
package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store is a thread-safe SQLite store for pipepie data.
type Store struct {
	db   *sql.DB
	
}

// New opens (or creates) a SQLite database at the given path.
func New(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite tuning
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma: %w", err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS tunnels (
    id         TEXT PRIMARY KEY,
    subdomain  TEXT UNIQUE NOT NULL,
    token      TEXT NOT NULL,
    created_at TEXT NOT NULL,
    last_seen  TEXT
);

CREATE TABLE IF NOT EXISTS requests (
    id           TEXT PRIMARY KEY,
    tunnel_id    TEXT NOT NULL REFERENCES tunnels(id),
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    query        TEXT NOT NULL DEFAULT '',
    req_headers  TEXT NOT NULL DEFAULT '{}',
    req_body     BLOB,
    req_size     INTEGER NOT NULL DEFAULT 0,
    status       TEXT NOT NULL DEFAULT 'pending',
    resp_status  INTEGER,
    resp_headers TEXT,
    resp_body    BLOB,
    duration_ms  INTEGER,
    error        TEXT,
    replay_of    TEXT,
    source_ip    TEXT,
    pipeline_id  TEXT NOT NULL DEFAULT '',
    step_name    TEXT NOT NULL DEFAULT '',
    trace_id     TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_requests_tunnel   ON requests(tunnel_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_requests_status   ON requests(status);
CREATE INDEX IF NOT EXISTS idx_requests_pipeline ON requests(pipeline_id, created_at) WHERE pipeline_id != '';
CREATE INDEX IF NOT EXISTS idx_requests_trace    ON requests(trace_id) WHERE trace_id != '';
`

// ── Tunnel CRUD ──────────────────────────────────────────────────────

// Tunnel represents a registered subdomain.
type Tunnel struct {
	ID        string
	Subdomain string
	Token     string
	CreatedAt string
	LastSeen  string
}

// TunnelRequestCounts returns request count per tunnel ID.
func (s *Store) TunnelRequestCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT tunnel_id, COUNT(*) FROM requests GROUP BY tunnel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int)
	for rows.Next() {
		var id string
		var n int
		rows.Scan(&id, &n)
		m[id] = n
	}
	return m, rows.Err()
}

// TunnelLastRequest returns the most recent request time per tunnel ID.
func (s *Store) TunnelLastRequest() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT tunnel_id, MAX(created_at) FROM requests GROUP BY tunnel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var id, ts string
		rows.Scan(&id, &ts)
		m[id] = ts
	}
	return m, rows.Err()
}

// TunnelRequestStats returns per-tunnel stats: total, success (2xx/3xx), errors (4xx/5xx/failed/timeout).
type TunnelStats struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Errors  int `json:"errors"`
}

func (s *Store) TunnelRequestStats() (map[string]TunnelStats, error) {
	rows, err := s.db.Query(`
		SELECT tunnel_id,
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN resp_status IS NOT NULL AND resp_status < 400 THEN 1 ELSE 0 END), 0) as ok,
			COALESCE(SUM(CASE WHEN status IN ('failed','timeout') OR (resp_status IS NOT NULL AND resp_status >= 400) THEN 1 ELSE 0 END), 0) as err
		FROM requests GROUP BY tunnel_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]TunnelStats)
	for rows.Next() {
		var id string
		var st TunnelStats
		rows.Scan(&id, &st.Total, &st.Success, &st.Errors)
		m[id] = st
	}
	return m, rows.Err()
}

// TunnelCreate registers a new tunnel and returns it.
func (s *Store) TunnelCreate(subdomain, token string) (*Tunnel, error) {
	t := &Tunnel{
		ID:        uuid.NewString(),
		Subdomain: subdomain,
		Token:     token,
		CreatedAt: now(),
	}
	_, err := s.db.Exec(
		`INSERT INTO tunnels (id, subdomain, token, created_at) VALUES (?,?,?,?)`,
		t.ID, t.Subdomain, t.Token, t.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create tunnel: %w", err)
	}
	return t, nil
}

// TunnelBySubdomain looks up a tunnel by its subdomain.
func (s *Store) TunnelBySubdomain(subdomain string) (*Tunnel, error) {
	row := s.db.QueryRow(`SELECT id, subdomain, token, created_at, last_seen FROM tunnels WHERE subdomain = ?`, subdomain)
	t := &Tunnel{}
	var lastSeen sql.NullString
	if err := row.Scan(&t.ID, &t.Subdomain, &t.Token, &t.CreatedAt, &lastSeen); err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		t.LastSeen = lastSeen.String
	}
	return t, nil
}

// TunnelList returns all registered tunnels.
func (s *Store) TunnelList() ([]*Tunnel, error) {
	rows, err := s.db.Query(`SELECT id, subdomain, token, created_at, last_seen FROM tunnels ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tunnels []*Tunnel
	for rows.Next() {
		t := &Tunnel{}
		var lastSeen sql.NullString
		if err := rows.Scan(&t.ID, &t.Subdomain, &t.Token, &t.CreatedAt, &lastSeen); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			t.LastSeen = lastSeen.String
		}
		tunnels = append(tunnels, t)
	}
	return tunnels, rows.Err()
}

// TunnelDelete removes a tunnel and its requests.
func (s *Store) TunnelDelete(subdomain string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Delete requests first (FK)
	_, err = tx.Exec(`DELETE FROM requests WHERE tunnel_id = (SELECT id FROM tunnels WHERE subdomain = ?)`, subdomain)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM tunnels WHERE subdomain = ?`, subdomain)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("tunnel %q not found", subdomain)
	}
	return tx.Commit()
}

// TunnelTouch updates the last_seen timestamp.
func (s *Store) TunnelTouch(subdomain string) {
	s.db.Exec(`UPDATE tunnels SET last_seen = ? WHERE subdomain = ?`, now(), subdomain)
}

// ── Request CRUD ─────────────────────────────────────────────────────

// SavedRequest represents a captured HTTP request with its response.
type SavedRequest struct {
	ID          string  `json:"id"`
	TunnelID    string  `json:"tunnel_id"`
	Method      string  `json:"method"`
	Path        string  `json:"path"`
	Query       string  `json:"query"`
	ReqHeaders  string  `json:"req_headers"`
	ReqBody     []byte  `json:"req_body,omitempty"`
	ReqSize     int     `json:"req_size"`
	Status      string  `json:"status"` // pending | forwarded | failed | timeout
	RespStatus  *int    `json:"resp_status,omitempty"`
	RespHeaders *string `json:"resp_headers,omitempty"`
	RespBody    []byte  `json:"resp_body,omitempty"`
	DurationMs  *int64  `json:"duration_ms,omitempty"`
	Error       *string `json:"error,omitempty"`
	ReplayOf    *string `json:"replay_of,omitempty"`
	SourceIP    string  `json:"source_ip"`
	PipelineID  string  `json:"pipeline_id,omitempty"`
	StepName    string  `json:"step_name,omitempty"`
	TraceID     string  `json:"trace_id,omitempty"`
	CreatedAt   string  `json:"created_at"`
}

// RequestInsertOpts holds optional fields for RequestInsert.
type RequestInsertOpts struct {
	PipelineID string
	StepName   string
	TraceID    string
}

// RequestInsert stores an incoming request. Returns the generated ID.
func (s *Store) RequestInsert(tunnelID, method, path, query, headers string, body []byte, sourceIP string, opts *RequestInsertOpts) (string, error) {
	id := uuid.NewString()
	pipelineID, stepName, traceID := "", "", ""
	if opts != nil {
		pipelineID = opts.PipelineID
		stepName = opts.StepName
		traceID = opts.TraceID
	}
	_, err := s.db.Exec(
		`INSERT INTO requests (id, tunnel_id, method, path, query, req_headers, req_body, req_size, status, source_ip, pipeline_id, step_name, trace_id, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, tunnelID, method, path, query, headers, body, len(body), "pending", sourceIP, pipelineID, stepName, traceID, now(),
	)
	return id, err
}

// RequestSetResponse updates a request with the forwarded response.
func (s *Store) RequestSetResponse(id string, status int, headers string, body []byte, durationMs int64) error {
	_, err := s.db.Exec(
		`UPDATE requests SET status='forwarded', resp_status=?, resp_headers=?, resp_body=?, duration_ms=? WHERE id=?`,
		status, headers, body, durationMs, id,
	)
	return err
}

// RequestSetError marks a request as failed.
func (s *Store) RequestSetError(id, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE requests SET status='failed', error=? WHERE id=?`,
		errMsg, id,
	)
	return err
}

// RequestSetTimeout marks a request as timed out.
func (s *Store) RequestSetTimeout(id string) error {
	_, err := s.db.Exec(`UPDATE requests SET status='timeout' WHERE id=?`, id)
	return err
}

// RequestGet retrieves a single request by ID.
func (s *Store) RequestGet(id string) (*SavedRequest, error) {
	row := s.db.QueryRow(`SELECT id, tunnel_id, method, path, query, req_headers, req_body, req_size, status, resp_status, resp_headers, resp_body, duration_ms, error, replay_of, source_ip, pipeline_id, step_name, trace_id, created_at FROM requests WHERE id=?`, id)
	return scanRequest(row)
}

// RequestList returns requests for a tunnel, newest first.
func (s *Store) RequestList(tunnelID string, limit, offset int) ([]*SavedRequest, int, error) {

	var total int
	s.db.QueryRow(`SELECT COUNT(*) FROM requests WHERE tunnel_id=?`, tunnelID).Scan(&total)

	rows, err := s.db.Query(
		`SELECT id, tunnel_id, method, path, query, req_headers, req_body, req_size, status, resp_status, resp_headers, resp_body, duration_ms, error, replay_of, source_ip, pipeline_id, step_name, trace_id, created_at
		 FROM requests WHERE tunnel_id=? ORDER BY created_at DESC LIMIT ? OFFSET ?`,
		tunnelID, limit, offset,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var reqs []*SavedRequest
	for rows.Next() {
		r, err := scanRequestRows(rows)
		if err != nil {
			return nil, 0, err
		}
		reqs = append(reqs, r)
	}
	return reqs, total, rows.Err()
}

// RequestPrune deletes requests older than the given duration.
func (s *Store) RequestPrune(retention time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-retention).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM requests WHERE created_at < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ── Pipeline traces ──────────────────────────────────────────────────

// TraceTimeline returns all requests in a trace, ordered chronologically.
func (s *Store) TraceTimeline(traceID string) ([]*SavedRequest, error) {
	rows, err := s.db.Query(
		`SELECT id, tunnel_id, method, path, query, req_headers, req_body, req_size, status, resp_status, resp_headers, resp_body, duration_ms, error, replay_of, source_ip, pipeline_id, step_name, trace_id, created_at
		 FROM requests WHERE trace_id=? ORDER BY created_at ASC`,
		traceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var reqs []*SavedRequest
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, r)
	}
	return reqs, rows.Err()
}

// PipelineTraces returns recent traces for a pipeline, grouped by trace_id.
func (s *Store) PipelineTraces(pipelineID string, limit int) ([]Trace, error) {
	if limit <= 0 {
		limit = 20
	}
	// Get distinct recent trace IDs
	rows, err := s.db.Query(
		`SELECT DISTINCT trace_id, MIN(created_at) as started
		 FROM requests WHERE pipeline_id=? AND trace_id != ''
		 GROUP BY trace_id ORDER BY started DESC LIMIT ?`,
		pipelineID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var traces []Trace
	for rows.Next() {
		var t Trace
		if err := rows.Scan(&t.TraceID, &t.StartedAt); err != nil {
			return nil, err
		}
		traces = append(traces, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Load steps for each trace
	for i := range traces {
		steps, err := s.TraceTimeline(traces[i].TraceID)
		if err != nil {
			return nil, err
		}
		traces[i].Steps = steps
		if len(steps) > 0 {
			traces[i].PipelineID = steps[0].PipelineID
			last := steps[len(steps)-1]
			traces[i].FinishedAt = last.CreatedAt
			// Check if any step failed
			traces[i].Status = "completed"
			for _, s := range steps {
				if s.Status == "failed" || s.Status == "timeout" {
					traces[i].Status = "failed"
					break
				}
				if s.Status == "pending" {
					traces[i].Status = "running"
				}
			}
			// Total duration
			if len(steps) >= 2 {
				// Parse times for duration calc
				t0, _ := time.Parse(time.RFC3339, steps[0].CreatedAt)
				t1, _ := time.Parse(time.RFC3339, last.CreatedAt)
				if last.DurationMs != nil {
					traces[i].TotalMs = t1.Sub(t0).Milliseconds() + *last.DurationMs
				} else {
					traces[i].TotalMs = t1.Sub(t0).Milliseconds()
				}
			} else if len(steps) == 1 && steps[0].DurationMs != nil {
				traces[i].TotalMs = *steps[0].DurationMs
			}
		}
	}
	return traces, nil
}

// Trace represents a pipeline execution (one run through all steps).
type Trace struct {
	TraceID    string         `json:"trace_id"`
	PipelineID string         `json:"pipeline_id"`
	Status     string         `json:"status"` // running | completed | failed
	StartedAt  string         `json:"started_at"`
	FinishedAt string         `json:"finished_at,omitempty"`
	TotalMs    int64          `json:"total_ms"`
	Steps      []*SavedRequest `json:"steps"`
}

// ── helpers ──────────────────────────────────────────────────────────

type scanner interface {
	Scan(dest ...any) error
}

func scanRequest(row scanner) (*SavedRequest, error) {
	r := &SavedRequest{}
	var respStatus sql.NullInt64
	var respHeaders, errMsg, replayOf sql.NullString
	var respBody []byte
	var durationMs sql.NullInt64

	err := row.Scan(
		&r.ID, &r.TunnelID, &r.Method, &r.Path, &r.Query,
		&r.ReqHeaders, &r.ReqBody, &r.ReqSize, &r.Status,
		&respStatus, &respHeaders, &respBody, &durationMs,
		&errMsg, &replayOf, &r.SourceIP,
		&r.PipelineID, &r.StepName, &r.TraceID, &r.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	if respStatus.Valid {
		v := int(respStatus.Int64)
		r.RespStatus = &v
	}
	if respHeaders.Valid {
		r.RespHeaders = &respHeaders.String
	}
	r.RespBody = respBody
	if durationMs.Valid {
		r.DurationMs = &durationMs.Int64
	}
	if errMsg.Valid {
		r.Error = &errMsg.String
	}
	if replayOf.Valid {
		r.ReplayOf = &replayOf.String
	}
	return r, nil
}

func scanRequestRows(rows *sql.Rows) (*SavedRequest, error) {
	return scanRequest(rows)
}

func now() string {
	return time.Now().UTC().Format(time.RFC3339)
}
