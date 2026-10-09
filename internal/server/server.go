package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"proxysql/internal/config"
	"proxysql/internal/sqlguard"
)

type Server struct {
	cfg  *config.Config
	pool *pgxpool.Pool
	mux  *http.ServeMux
	// minimal metrics
	queriesTotal   atomic.Uint64
	queriesErr     atomic.Uint64
	queriesTimeout atomic.Uint64
}

type queryRequest struct {
	SQL       string `json:"sql"`
	Params    []any  `json:"params"`
	TimeoutMs *int64 `json:"timeout_ms"`
	MaxRows   *int   `json:"max_rows"`
}

type columnMeta struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type queryResponse struct {
	RequestID  string       `json:"request_id"`
	Columns    []columnMeta `json:"columns"`
	Rows       [][]any      `json:"rows"`
	RowCount   int          `json:"row_count"`
	Truncated  bool         `json:"truncated"`
	DurationMs int64        `json:"duration_ms"`
}

type errResponse struct {
	Error     string `json:"error"`
	RequestID string `json:"request_id,omitempty"`
}

func New(cfg *config.Config, pool *pgxpool.Pool) *Server {
	s := &Server{cfg: cfg, pool: pool, mux: http.NewServeMux()}
	base := normalizeBase(cfg.Server.BasePath)
	s.mux.HandleFunc(base+"/v1/query/sync", s.handleQuerySync)
	s.mux.HandleFunc(base+"/healthz", s.handleHealthz)
	s.mux.HandleFunc(base+"/readyz", s.handleReadyz)
	s.mux.HandleFunc(base+"/metrics", s.handleMetrics)
	if base != "/" {
		// Explicit 404 for bare "/" when behind a sub-path, avoids confusion.
		s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			writeErr(w, http.StatusNotFound, "not found, use "+base+" prefix", "")
		})
	}
	return s
}

func (s *Server) Handler() http.Handler { return s.mux }

func normalizeBase(b string) string {
	b = strings.TrimSpace(b)
	if b == "" || b == "/" {
		return ""
	}
	if !strings.HasPrefix(b, "/") {
		b = "/" + b
	}
	return strings.TrimSuffix(b, "/")
}

func (s *Server) checkAuth(r *http.Request) bool {
	if len(s.cfg.Auth.APIKeys) == 0 {
		return true
	}
	key := r.Header.Get("X-API-Key")
	if key == "" {
		if ah := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(ah), "bearer ") {
			key = strings.TrimSpace(ah[7:])
		}
	}
	for _, k := range s.cfg.Auth.APIKeys {
		if k != "" && key == k {
			return true
		}
	}
	return false
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	if err := s.pool.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed", "")
		return
	}
	st := s.pool.Stat()
	fmt.Fprintf(w, "# HELP proxysql_queries_total total sync queries\n# TYPE proxysql_queries_total counter\nproxysql_queries_total %d\n", s.queriesTotal.Load())
	fmt.Fprintf(w, "proxysql_queries_error_total %d\n", s.queriesErr.Load())
	fmt.Fprintf(w, "proxysql_queries_timeout_total %d\n", s.queriesTimeout.Load())
	fmt.Fprintf(w, "proxysql_pool_acquired %d\nproxysql_pool_idle %d\nproxysql_pool_total %d\nproxysql_pool_max %d\n",
		st.AcquiredConns(), st.IdleConns(), st.TotalConns(), st.MaxConns())
}

func (s *Server) handleQuerySync(w http.ResponseWriter, r *http.Request) {
	reqID := newID()
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "only POST is allowed", reqID)
		return
	}
	if !s.checkAuth(r) {
		writeErr(w, http.StatusUnauthorized, "unauthorized", reqID)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.Server.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.UseNumber() // keep exact literal text; normalized to text params below
	var q queryRequest
	if err := dec.Decode(&q); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json: "+err.Error(), reqID)
		return
	}
	sql := strings.TrimSpace(q.SQL)
	if sql == "" {
		writeErr(w, http.StatusBadRequest, "sql is required", reqID)
		return
	}
	nParams := len(q.Params)
	if err := sqlguard.CheckRawSQL(sql, nParams, s.cfg.Query.AllowRawSQL); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error(), reqID)
		return
	}
	if !s.cfg.Query.AllowMultiStatements && sqlguard.HasMultiStatements(sql) {
		writeErr(w, http.StatusBadRequest, "multi statements are not allowed", reqID)
		return
	}
	stmt := sqlguard.Classify(sql)
	if !sqlguard.Allowed(stmt, s.cfg.Query.AllowedStatements) {
		writeErr(w, http.StatusForbidden, "statement "+stmt+" not allowed", reqID)
		return
	}
	if !s.cfg.Query.AllowRawSQL && isDML(stmt) && sqlguard.ContainsStringLiteral(sql) {
		writeErr(w, http.StatusBadRequest, "string literals are not allowed, use $n params (or enable query.allow_raw_sql)", reqID)
		return
	}

	timeout := s.cfg.Query.DefaultTimeout.Duration
	if q.TimeoutMs != nil {
		if *q.TimeoutMs <= 0 {
			writeErr(w, http.StatusBadRequest, "timeout_ms must be > 0", reqID)
			return
		}
		timeout = time.Duration(*q.TimeoutMs) * time.Millisecond
		if timeout > s.cfg.Query.MaxTimeout.Duration {
			writeErr(w, http.StatusBadRequest, "timeout exceeds server max_timeout", reqID)
			return
		}
	}
	maxRows := s.cfg.Query.MaxRows
	if q.MaxRows != nil {
		if *q.MaxRows <= 0 {
			writeErr(w, http.StatusBadRequest, "max_rows must be > 0", reqID)
			return
		}
		if *q.MaxRows < maxRows {
			maxRows = *q.MaxRows
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	if s.cfg.Log.LogSQLText {
		slog.Info("query start", "request_id", reqID, "stmt", stmt, "sql", sql)
	}
	start := time.Now()
	s.queriesTotal.Add(1)

	args := make([]any, len(q.Params))
	for i, p := range q.Params {
		args[i] = normalizeParam(p)
	}

	noteErr := func(ctx context.Context) {
		s.queriesErr.Add(1)
		if ctx.Err() == context.DeadlineExceeded {
			s.queriesTimeout.Add(1)
		}
	}

	rows, err := s.pool.Query(ctx, sql, args...)
	if err != nil {
		noteErr(ctx)
		writeErr(w, mapPGError(ctx, err), pgMessage(err), reqID)
		return
	}
	defer rows.Close()

	cols := rows.FieldDescriptions()
	typeMap := pgtype.NewMap()
	outCols := make([]columnMeta, len(cols))
	for i, c := range cols {
		outCols[i] = columnMeta{Name: c.Name, Type: typeName(typeMap, c.DataTypeOID)}
	}
	outRows := make([][]any, 0, 64)
	truncated := false
	count := 0
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			noteErr(ctx)
			writeErr(w, mapPGError(ctx, err), pgMessage(err), reqID)
			return
		}
		nr := make([]any, len(vals))
		for i, v := range vals {
			nr[i] = normalizeValue(v, cols[i].DataTypeOID)
		}
		outRows = append(outRows, nr)
		count++
		if count >= maxRows {
			// Drain with cancel to free conn fast; mark truncated if more remain.
			if rows.Next() {
				truncated = true
			}
			break
		}
	}
	if err := rows.Err(); err != nil {
		noteErr(ctx)
		writeErr(w, mapPGError(ctx, err), pgMessage(err), reqID)
		return
	}
	// DDL (e.g. CREATE) returns no rows: report affected via CommandTag.
	rowCount := count
	if len(cols) == 0 {
		rowCount = int(rows.CommandTag().RowsAffected())
	}
	writeJSON(w, http.StatusOK, queryResponse{
		RequestID: reqID, Columns: outCols, Rows: outRows,
		RowCount: rowCount, Truncated: truncated,
		DurationMs: time.Since(start).Milliseconds(),
	})
}

func mapPGError(ctx context.Context, err error) int {
	if ctx.Err() == context.DeadlineExceeded {
		return http.StatusRequestTimeout
	}
	if ctx.Err() == context.Canceled {
		return 499 // client closed (non-standard but common)
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "syntax error"),
		strings.Contains(msg, "invalid"),
		strings.Contains(msg, "does not exist"),
		strings.Contains(msg, "duplicate"),
		strings.Contains(msg, "violates"):
		return http.StatusBadRequest
	case strings.Contains(msg, "too many clients"),
		strings.Contains(msg, "connection"):
		return http.StatusServiceUnavailable
	}
	return http.StatusBadGateway
}

func pgMessage(err error) string { return err.Error() }

// typeName maps a PostgreSQL OID to its lowercase type name.
func typeName(m *pgtype.Map, oid uint32) string {
	if m != nil {
		if t, ok := m.TypeForOID(oid); ok && t != nil && t.Name != "" {
			return strings.ToLower(t.Name)
		}
	}
	return fmt.Sprintf("oid:%d", oid)
}

// normalizeValue converts pgx/pgtype values into JSON-safe primitives.
func normalizeValue(v any, oid uint32) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case []byte:
		if oid == 17 { // bytea
			return base64.StdEncoding.EncodeToString(t)
		}
		return string(t)
	case string, bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, float32, float64:
		return t
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case pgtype.Numeric:
		if t.NaN {
			return "NaN"
		}
		if t.InfinityModifier != pgtype.Finite {
			if t.InfinityModifier > 0 {
				return "Infinity"
			}
			return "-Infinity"
		}
		if !t.Valid {
			return nil
		}
		// MarshalJSON emits the exact decimal literal; keep it as
		// json.Number so precision survives the outer encoding.
		if b, err := t.MarshalJSON(); err == nil {
			return json.Number(strings.TrimSpace(string(b)))
		}
		if f, err := t.Float64Value(); err == nil && f.Valid {
			return f.Float64
		}
		return fmt.Sprintf("%v", t)
	case *pgtype.Numeric:
		if t == nil {
			return nil
		}
		return normalizeValue(*t, oid)
	case pgtype.Date:
		if !t.Valid {
			return nil
		}
		if t.InfinityModifier != pgtype.Finite {
			if t.InfinityModifier > 0 {
				return "infinity"
			}
			return "-infinity"
		}
		return t.Time.Format("2006-01-02")
	case pgtype.Timestamp:
		if !t.Valid {
			return nil
		}
		if t.InfinityModifier != pgtype.Finite {
			if t.InfinityModifier > 0 {
				return "infinity"
			}
			return "-infinity"
		}
		return t.Time.Format(time.RFC3339Nano)
	case pgtype.Timestamptz:
		if !t.Valid {
			return nil
		}
		if t.InfinityModifier != pgtype.Finite {
			if t.InfinityModifier > 0 {
				return "infinity"
			}
			return "-infinity"
		}
		return t.Time.Format(time.RFC3339Nano)
	case pgtype.Time:
		if !t.Valid {
			return nil
		}
		us := t.Microseconds
		h := us / 3600_000_000
		m := (us % 3600_000_000) / 60_000_000
		s := (us % 60_000_000) / 1_000_000
		rem := us % 1_000_000
		if rem == 0 {
			return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
		}
		return fmt.Sprintf("%02d:%02d:%02d.%06d", h, m, s, rem)
	case pgtype.Interval:
		if !t.Valid {
			return nil
		}
		return fmt.Sprintf("%d months %d days %d microseconds", t.Months, t.Days, t.Microseconds)
	default:
		// pgx may return [16]byte for uuid, net.IPNet, slices, maps...
		if b, ok := v.([16]byte); ok {
			var u [16]byte = b
			hexb := hex.EncodeToString(u[:])
			return fmt.Sprintf("%s-%s-%s-%s-%s", hexb[0:8], hexb[8:12], hexb[12:16], hexb[16:20], hexb[20:])
		}
		if _, ok := v.([]any); ok {
			return v
		}
		// Fallback: let encoding/json handle maps/slices/structs; stringify numerics safely.
		switch t.(type) {
		case json.Number:
			return t
		}
		if _, err := json.Marshal(v); err == nil {
			return v
		}
		return fmt.Sprintf("%v", v)
	}
}

func isDML(stmt string) bool {
	switch stmt {
	case "SELECT", "INSERT", "UPDATE", "DELETE":
		return true
	}
	return false
}

// normalizeParam converts JSON-decoded params to text-friendly values.
//
// Rationale: pgx has no text-format encode plan for float64, so a JSON
// number sent as float64 fails against unknown-type ($1-as-text) params.
// Sending every scalar as its literal text lets PostgreSQL coerce by
// context (int/float/bool/uuid/timestamptz/jsonb all parse from text).
// Objects/arrays become compact JSON text (good for jsonb columns;
// for native PG arrays use an explicit '{1,2}' string + ::type cast).
func normalizeParam(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case json.Number:
		return strings.TrimSpace(string(t))
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any, map[string]any:
		if b, err := json.Marshal(t); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", t)
	default:
		return fmt.Sprintf("%v", t)
	}
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg, reqID string) {
	if code == 499 {
		code = 499 // Go allows custom code via WriteHeader
	}
	writeJSON(w, code, errResponse{Error: msg, RequestID: reqID})
}

var _ = pgx.ErrNoRows
