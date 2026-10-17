package testhelper

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/api"
	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/store"
)

type Env struct {
	API    *api.Server
	Store  *store.Store
	Server *httptest.Server
}

func isolatedDatabaseURL(t *testing.T, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	name := strings.TrimPrefix(u.Path, "/")
	if name == "" {
		t.Fatalf("test database URL has no database name")
	}
	pkg := "t"
	for i := 1; i < 10; i++ {
		_, file, _, ok := runtime.Caller(i)
		if !ok {
			break
		}
		if dir := filepath.Base(filepath.Dir(file)); dir != "testhelper" {
			pkg = dir
			break
		}
	}
	var b strings.Builder
	for _, r := range strings.ToLower(pkg) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	suffix := strings.Trim(b.String(), "_")
	if suffix == "" {
		suffix = "t"
	}
	isolated := name + "_" + suffix
	if len(isolated) > 63 {
		isolated = isolated[:63]
	}
	u.Path = "/" + isolated
	return u.String()
}

func Boot(t *testing.T, mutate func(*config.Config)) *Env {
	t.Helper()
	databaseURL := os.Getenv("CHARTDB_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("CHARTDB_TEST_DATABASE_URL is not set")
	}
	databaseURL = isolatedDatabaseURL(t, databaseURL)
	ctx := context.Background()
	if err := store.EnsureDatabase(ctx, databaseURL); err != nil {
		t.Fatalf("ensure test database: %v", err)
	}
	st, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}
	cfg := config.Load()
	cfg.DatabaseURL = databaseURL
	cfg.CookieSecure = false
	cfg.LoginPerMinute = 10000
	cfg.RegisterPerHour = 10000
	cfg.ArgonMemory = 8 * 1024
	cfg.ArgonTime = 1
	cfg.ArgonThreads = 1
	cfg.AppURL = "http://example.test"
	if mutate != nil {
		mutate(&cfg)
	}
	srv := api.New(cfg, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &Env{API: srv, Store: st, Server: ts}
}

func (e *Env) Client() *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar}
}

func Do(t *testing.T, client *http.Client, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var reader *bytes.Reader
	if body == nil {
		reader = bytes.NewReader(nil)
	} else {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer resp.Body.Close()
	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	if decoded == nil {
		decoded = map[string]any{}
	}
	return resp.StatusCode, decoded
}

func (e *Env) Register(t *testing.T, client *http.Client, email, password string) string {
	t.Helper()
	status, body := Do(t, client, "POST", e.Server.URL+"/api/v1/auth/register", map[string]string{"email": email, "password": password})
	if status != http.StatusCreated {
		t.Fatalf("register %s: status %d body %v", email, status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("register %s: missing account id in %v", email, body)
	}
	return id
}

func UniqueEmail(prefix string) string {
	return prefix + "-" + uuid.NewString()[:8] + "@example.test"
}

func (e *Env) ExpiredSession(t *testing.T, accountID string) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("random token: %v", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	_, err := e.Store.DB.ExecContext(context.Background(), `INSERT INTO session (id, account_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`, uuid.New(), accountID, hex.EncodeToString(sum[:]), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("insert expired session: %v", err)
	}
	return token
}
