package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/apierror"
	"github.com/anggasct/chartdb/server/auth"
	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/diagram"
	"github.com/anggasct/chartdb/server/ratelimit"
	"github.com/anggasct/chartdb/server/session"
	"github.com/anggasct/chartdb/server/share"
	"github.com/anggasct/chartdb/server/store"
	"github.com/anggasct/chartdb/server/sync"
)

type Server struct {
	cfg             config.Config
	store           *store.Store
	authSvc         *auth.Service
	diagramSvc      *diagram.Service
	shareSvc        *share.Service
	syncHub         *sync.Hub
	loginLimiter    *ratelimit.Limiter
	registerLimiter *ratelimit.Limiter
}

func New(cfg config.Config, st *store.Store) *Server {
	if cfg.MaxDocBytes <= 0 {
		cfg.MaxDocBytes = config.DefaultMaxDocBytes
	}
	s := &Server{
		cfg:             cfg,
		store:           st,
		diagramSvc:      diagram.NewService(st.DB, cfg.MaxDocBytes),
		shareSvc:        share.NewService(st.DB),
		syncHub:         sync.NewHub(st.DB, cfg.MaxWSMsgBytes, cfg.MaxConnPerDiagram, cfg.SnapshotEveryN, cfg.AwarenessTimeout),
		loginLimiter:    ratelimit.New(cfg.LoginPerMinute, time.Minute),
		registerLimiter: ratelimit.New(cfg.RegisterPerHour, time.Hour),
	}
	s.authSvc = auth.NewService(st.DB, cfg.SessionLifetime, cfg.ArgonMemory, cfg.ArgonTime, cfg.ArgonThreads, map[string]auth.Provider{})
	return s
}

func (s *Server) AddProvider(id string, p auth.Provider) {
	s.authSvc.Providers[id] = p
}

func (s *Server) SyncHub() *sync.Hub {
	return s.syncHub
}

func clientIPFrom(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first, _, _ := strings.Cut(xff, ","); strings.TrimSpace(first) != "" {
				return strings.TrimSpace(first)
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) clientIP(r *http.Request) string {
	return clientIPFrom(r, s.cfg.TrustProxyHeaders)
}

func userAgent(r *http.Request) string {
	ua := r.UserAgent()
	if len(ua) > 512 {
		return ua[:512]
	}
	return ua
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func redactLogPath(p string) string {
	const prefix = "/api/v1/s/"
	if strings.HasPrefix(p, prefix) && len(p) > len(prefix) {
		return prefix + "[redacted]"
	}
	return p
}

func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		attrs := []any{"method", r.Method, "path", redactLogPath(r.URL.Path), "status", sw.status, "duration_ms", time.Since(start).Milliseconds()}
		if sess, ok := r.Context().Value(sessionKey{}).(session.Session); ok {
			attrs = append(attrs, "account_id", sess.AccountID.String())
		}
		slog.Info("request", attrs...)
	})
}

type sessionKey struct{}

func (s *Server) optionalAccount(r *http.Request) *uuid.UUID {
	cookie, err := r.Cookie(session.CookieName)
	if err != nil {
		return nil
	}
	sess, err := session.Validate(r.Context(), s.store.DB, cookie.Value)
	if err != nil {
		return nil
	}
	return &sess.AccountID
}

func (s *Server) withAuth(next func(http.ResponseWriter, *http.Request, session.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(session.CookieName)
		if err != nil {
			apierror.Write(w, r, apierror.Unauthorized("unauthorized", "login required"))
			return
		}
		sess, err := session.Validate(r.Context(), s.store.DB, cookie.Value)
		if err != nil {
			apierror.Write(w, r, apierror.Unauthorized("unauthorized", "login required"))
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, sess)), sess)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_body", "request body must be valid JSON"))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.store.Ping(r.Context()); err != nil {
			apierror.Write(w, r, apierror.Unavailable())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("GET /api/v1/auth/login/oidc", s.handleOIDCBegin)
	mux.HandleFunc("GET /api/v1/auth/callback", s.handleOIDCCallback)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.HandleFunc("GET /api/v1/me", s.withAuth(s.handleMe))
	mux.HandleFunc("GET /api/v1/auth/session", s.withAuth(s.handleSessionList))
	mux.HandleFunc("DELETE /api/v1/auth/session/{id}", s.withAuth(s.handleSessionRevoke))
	mux.HandleFunc("POST /api/v1/auth/password", s.withAuth(s.handlePasswordChange))

	mux.HandleFunc("GET /api/v1/diagram", s.withAuth(s.handleDiagramList))
	mux.HandleFunc("POST /api/v1/diagram", s.withAuth(s.handleDiagramCreate))
	mux.HandleFunc("GET /api/v1/diagram/{id}", s.withAuth(s.handleDiagramGet))
	mux.HandleFunc("PUT /api/v1/diagram/{id}", s.withAuth(s.handleDiagramPut))
	mux.HandleFunc("DELETE /api/v1/diagram/{id}", s.withAuth(s.handleDiagramDelete))
	mux.HandleFunc("GET /api/v1/diagram/{id}/version", s.withAuth(s.handleVersionList))
	mux.HandleFunc("GET /api/v1/diagram/{id}/version/{n}", s.withAuth(s.handleVersionGet))
	mux.HandleFunc("POST /api/v1/diagram/{id}/restore", s.withAuth(s.handleRestore))
	mux.HandleFunc("POST /api/v1/diagram/{id}/share", s.withAuth(s.handleShareCreate))
	mux.HandleFunc("GET /api/v1/diagram/{id}/share", s.withAuth(s.handleShareList))
	mux.HandleFunc("DELETE /api/v1/diagram/{id}/share/{shareId}", s.withAuth(s.handleShareRevoke))
	mux.HandleFunc("GET /api/v1/s/{token}", s.handleShareRead)
	mux.HandleFunc("GET /api/v1/diagram/{id}/sync", s.handleSync)

	return s.withLogging(mux)
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !s.registerLimiter.Allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "3600")
		apierror.Write(w, r, apierror.TooManyRequests())
		return
	}
	var body struct {
		Email       string `json:"email"`
		Password    string `json:"password"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	acc, token, err := s.authSvc.Register(r.Context(), body.Email, body.Password, body.DisplayName, userAgent(r), s.clientIP(r))
	if err != nil {
		if apiErr, ok := err.(*apierror.Error); ok && apiErr.Status == 409 {
			slog.Warn("register conflict", "email", body.Email)
		}
		apierror.Write(w, r, err)
		return
	}
	session.SetCookie(w, token, s.cfg.CookieSecure, s.cfg.SessionLifetime)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(acc)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.Allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		apierror.Write(w, r, apierror.TooManyRequests())
		slog.Warn("login rate limited", "ip", s.clientIP(r))
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	acc, token, err := s.authSvc.Login(r.Context(), body.Email, body.Password, userAgent(r), s.clientIP(r))
	if err != nil {
		slog.Warn("login failed")
		apierror.Write(w, r, err)
		return
	}
	session.SetCookie(w, token, s.cfg.CookieSecure, s.cfg.SessionLifetime)
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleOIDCBegin(w http.ResponseWriter, r *http.Request) {
	providerID := r.URL.Query().Get("provider")
	if providerID == "" {
		apierror.Write(w, r, apierror.BadRequest("provider_required", "choose a login provider"))
		return
	}
	url, err := s.authSvc.BeginLogin(providerID, s.optionalAccount(r))
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if !s.loginLimiter.Allow(s.clientIP(r)) {
		w.Header().Set("Retry-After", "60")
		apierror.Write(w, r, apierror.TooManyRequests())
		return
	}
	query := r.URL.Query()
	if errParam := query.Get("error"); errParam != "" {
		apierror.Write(w, r, apierror.BadRequest("oidc_failed", "provider login failed"))
		return
	}
	var current *uuid.UUID
	if cookie, err := r.Cookie(session.CookieName); err == nil {
		if sess, err := session.Validate(r.Context(), s.store.DB, cookie.Value); err == nil {
			current = &sess.AccountID
		}
	}
	acc, token, err := s.authSvc.Callback(r.Context(), query.Get("state"), query.Get("code"), current, userAgent(r), s.clientIP(r))
	if err != nil {
		slog.Warn("oidc callback failed")
		apierror.Write(w, r, err)
		return
	}
	_ = acc
	session.SetCookie(w, token, s.cfg.CookieSecure, s.cfg.SessionLifetime)
	http.Redirect(w, r, s.cfg.AppURL+"/diagrams", http.StatusFound)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(session.CookieName)
	if err != nil {
		session.ClearCookie(w, s.cfg.CookieSecure)
		apierror.Write(w, r, apierror.Unauthorized("unauthorized", "login required"))
		return
	}
	if err := session.RevokeByToken(r.Context(), s.store.DB, cookie.Value); err != nil {
		apierror.Write(w, r, apierror.Internal())
		return
	}
	session.ClearCookie(w, s.cfg.CookieSecure)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, sess session.Session) {
	acc, identities, err := s.authSvc.Me(r.Context(), sess.AccountID)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"account": acc, "identities": identities})
}

func (s *Server) handleSessionList(w http.ResponseWriter, r *http.Request, sess session.Session) {
	infos, err := session.List(r.Context(), s.store.DB, sess.AccountID)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": infos})
}

func (s *Server) handleSessionRevoke(w http.ResponseWriter, r *http.Request, sess session.Session) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "session id is not a valid UUID"))
		return
	}
	if err := session.Revoke(r.Context(), s.store.DB, id, sess.AccountID); err != nil {
		apierror.Write(w, r, apierror.NotFound("not_found", "session not found"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request, sess session.Session) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if err := s.authSvc.ChangePassword(r.Context(), sess.AccountID, body.CurrentPassword, body.NewPassword); err != nil {
		apierror.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func readDocBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, max+1)
	defer r.Body.Close()
	raw, err := io.ReadAll(r.Body)
	if err != nil || int64(len(raw)) > max {
		apierror.Write(w, r, apierror.TooLarge("diagram_too_large", fmt.Sprintf("diagram exceeds the %dMiB limit", max>>20)))
		return nil, false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		apierror.Write(w, r, apierror.BadRequest("invalid_document", "diagram body must be a single JSON document"))
		return nil, false
	}
	return raw, true
}

type saveResponse struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title,omitempty"`
	Version   int       `json:"version"`
	Epoch     int       `json:"epoch"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (s *Server) handleDiagramCreate(w http.ResponseWriter, r *http.Request, sess session.Session) {
	raw, ok := readDocBody(w, r, s.cfg.MaxDocBytes)
	if !ok {
		return
	}
	var envelope struct {
		Title    string          `json:"title"`
		Document json.RawMessage `json:"document"`
	}
	var title string
	var doc []byte
	if err := json.Unmarshal(raw, &envelope); err == nil && len(envelope.Document) > 0 {
		title = envelope.Title
		doc = envelope.Document
	} else {
		doc = raw
	}
	summary, err := s.diagramSvc.Create(r.Context(), sess.AccountID, title, doc)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, saveResponse{ID: summary.ID, Title: summary.Title, Version: summary.Version, Epoch: summary.Epoch, CreatedAt: summary.CreatedAt, UpdatedAt: summary.UpdatedAt})
}

func (s *Server) handleDiagramList(w http.ResponseWriter, r *http.Request, sess session.Session) {
	query := r.URL.Query()
	limit := diagram.DefaultListLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			apierror.Write(w, r, apierror.BadRequest("invalid_limit", "limit must be a number"))
			return
		}
		limit = n
	}
	items, next, err := s.diagramSvc.List(r.Context(), sess.AccountID, limit, query.Get("cursor"))
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"diagrams": items, "next_cursor": next})
}

func (s *Server) handleDiagramGet(w http.ResponseWriter, r *http.Request, sess session.Session) {
	d, err := s.diagramSvc.Get(r.Context(), sess.AccountID, r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleDiagramPut(w http.ResponseWriter, r *http.Request, sess session.Session) {
	raw, ok := readDocBody(w, r, s.cfg.MaxDocBytes)
	if !ok {
		return
	}
	force := r.URL.Query().Has("force")
	if !force {
		if s.syncHub.PeerCount(r.PathValue("id")) > 0 {
			apierror.Write(w, r, apierror.Conflict("editors_connected", "editors are connected, confirm replace"))
			return
		}
	}
	summary, err := s.diagramSvc.PutWithForce(r.Context(), sess.AccountID, r.PathValue("id"), raw, force)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	if force {
		diagramID, err := uuid.Parse(r.PathValue("id"))
		if err == nil {
			base := sync.BaseUpdateFromJSON(canonicalForSync(raw))
			_ = s.resetRoom(diagramID, summary.Epoch, base)
		}
	}
	writeJSON(w, http.StatusOK, saveResponse{ID: summary.ID, Version: summary.Version, Epoch: summary.Epoch, UpdatedAt: summary.UpdatedAt})
}

func (s *Server) resetRoom(diagramID uuid.UUID, epoch int, base []byte) error {
	ctx := context.Background()
	room, err := s.syncHub.GetOrLoad(ctx, diagramID)
	if err != nil {
		return err
	}
	freshState := base
	if len(freshState) == 0 {
		freshState = sync.BaseUpdateFromJSON([]byte(`{}`))
	}
	var maxSeq int64
	_ = s.store.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM sync_update WHERE diagram_id = $1`, diagramID).Scan(&maxSeq)
	_, _ = s.store.DB.ExecContext(ctx, `INSERT INTO sync_snapshot (diagram_id, update, at_seq) VALUES ($1, $2, $3) ON CONFLICT (diagram_id) DO UPDATE SET update = EXCLUDED.update, at_seq = EXCLUDED.at_seq, created_at = now()`, diagramID, freshState, maxSeq)
	room.ResetForForce(freshState, epoch, maxSeq)
	return nil
}

func (s *Server) handleDiagramDelete(w http.ResponseWriter, r *http.Request, sess session.Session) {
	if err := s.diagramSvc.Delete(r.Context(), sess.AccountID, r.PathValue("id")); err != nil {
		apierror.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleVersionList(w http.ResponseWriter, r *http.Request, sess session.Session) {
	versions, err := s.diagramSvc.Versions(r.Context(), sess.AccountID, r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *Server) handleVersionGet(w http.ResponseWriter, r *http.Request, sess session.Session) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		apierror.Write(w, r, apierror.BadRequest("invalid_version", "version must be a positive number"))
		return
	}
	v, err := s.diagramSvc.VersionDoc(r.Context(), sess.AccountID, r.PathValue("id"), n)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request, sess session.Session) {
	var body struct {
		Version int `json:"version"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	summary, err := s.diagramSvc.Restore(r.Context(), sess.AccountID, r.PathValue("id"), body.Version)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, saveResponse{ID: summary.ID, Version: summary.Version, Epoch: summary.Epoch, UpdatedAt: summary.UpdatedAt})
}

func (s *Server) handleShareCreate(w http.ResponseWriter, r *http.Request, sess session.Session) {
	diagramID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "diagram id is not a valid UUID"))
		return
	}
	var body struct {
		ExpiresAt *time.Time `json:"expires_at"`
	}
	if r.ContentLength != 0 {
		if !decodeJSON(w, r, &body) {
			return
		}
	}
	created, err := s.shareSvc.Create(r.Context(), sess.AccountID, diagramID, body.ExpiresAt)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleShareList(w http.ResponseWriter, r *http.Request, sess session.Session) {
	diagramID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "diagram id is not a valid UUID"))
		return
	}
	links, err := s.shareSvc.List(r.Context(), sess.AccountID, diagramID)
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": links})
}

func (s *Server) handleShareRevoke(w http.ResponseWriter, r *http.Request, sess session.Session) {
	diagramID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "diagram id is not a valid UUID"))
		return
	}
	shareID, err := uuid.Parse(r.PathValue("shareId"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "share id is not a valid UUID"))
		return
	}
	if err := s.shareSvc.Revoke(r.Context(), sess.AccountID, diagramID, shareID); err != nil {
		apierror.Write(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleShareRead(w http.ResponseWriter, r *http.Request) {
	doc, err := s.shareSvc.ReadPublic(r.Context(), r.PathValue("token"))
	if err != nil {
		apierror.Write(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func canonicalForSync(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func (s *Server) handleSync(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(session.CookieName)
	if err != nil {
		apierror.Write(w, r, apierror.Unauthorized("unauthorized", "login required"))
		return
	}
	sess, err := session.Validate(r.Context(), s.store.DB, cookie.Value)
	if err != nil {
		apierror.Write(w, r, apierror.Unauthorized("unauthorized", "login required"))
		return
	}
	diagramID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		apierror.Write(w, r, apierror.BadRequest("invalid_id", "diagram id is not a valid UUID"))
		return
	}
	var ownerID uuid.UUID
	err = s.store.DB.QueryRowContext(r.Context(), `SELECT owner_account_id FROM diagram WHERE id = $1`, diagramID).Scan(&ownerID)
	if err != nil {
		apierror.Write(w, r, apierror.NotFound("not_found", "diagram not found"))
		return
	}
	if ownerID != sess.AccountID {
		apierror.Write(w, r, apierror.Forbidden("forbidden", "you do not own this diagram"))
		return
	}
	s.syncHub.ServeWS(w, r, diagramID, sess.AccountID)
}
