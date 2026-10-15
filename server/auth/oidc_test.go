package auth_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/auth"
	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/testhelper"
)

func uniqueSuffix() string {
	return uuid.NewString()[:8]
}

type stubIdP struct {
	key      *rsa.PrivateKey
	email    string
	verified bool
	sub      string
}

func (s *stubIdP) sign(payload map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test-key", "typ": "JWT"})
	body, _ := json.Marshal(payload)
	head := base64.RawURLEncoding.EncodeToString(header)
	middle := base64.RawURLEncoding.EncodeToString(body)
	digest := sha256.Sum256([]byte(head + "." + middle))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, digest[:])
	if err != nil {
		panic(err)
	}
	return head + "." + middle + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (s *stubIdP) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                issuer,
			"authorization_endpoint":                issuer + "/authorize",
			"token_endpoint":                        issuer + "/token",
			"jwks_uri":                              issuer + "/jwks",
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"keys": []any{map[string]any{
				"kty": "RSA",
				"kid": "test-key",
				"use": "sig",
				"alg": "RS256",
				"n":   base64.RawURLEncoding.EncodeToString(s.key.N.Bytes()),
				"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes()),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		issuer := "http://" + r.Host
		w.Header().Set("Content-Type", "application/json")
		now := time.Now()
		idToken := s.sign(map[string]any{
			"iss": issuer, "sub": s.sub, "aud": "test-client",
			"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(),
			"email": s.email, "email_verified": s.verified, "name": "IdP User",
		})
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "stub-access", "token_type": "Bearer",
			"expires_in": 3600, "id_token": idToken,
		})
	})
	return mux
}

func TestOIDCRealProviderRoundtrip(t *testing.T) {
	env := testhelper.Boot(t, nil)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	idp := &stubIdP{key: key, email: "idp-user@example.test", verified: true, sub: "idp-sub-" + uniqueSuffix()}
	server := httptest.NewServer(idp.handler())
	defer server.Close()

	provider, err := auth.NewGOIDCProvider(t.Context(), config.OIDCProvider{
		ID: "stub", Issuer: server.URL, ClientID: "test-client",
		ClientSecret: "secret", RedirectURL: "http://example.test/callback",
	})
	if err != nil {
		t.Fatalf("configure provider: %v", err)
	}
	svc := auth.NewService(env.Store.DB, time.Hour, 8192, 1, 1, map[string]auth.Provider{"stub": provider})

	authURL, err := svc.BeginLogin("stub")
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	state := ""
	for _, part := range strings.Split(strings.SplitN(authURL, "?", 2)[1], "&") {
		if strings.HasPrefix(part, "state=") {
			state = strings.TrimPrefix(part, "state=")
		}
	}
	if state == "" {
		t.Fatalf("auth url missing state: %s", authURL)
	}
	first, _, err := svc.Callback(t.Context(), state, "stub-code", nil, "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("callback: %v", err)
	}
	if first.PrimaryEmail == nil || *first.PrimaryEmail != "idp-user@example.test" {
		t.Fatalf("account email %v", first.PrimaryEmail)
	}

	authURL, err = svc.BeginLogin("stub")
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	state = ""
	for _, part := range strings.Split(strings.SplitN(authURL, "?", 2)[1], "&") {
		if strings.HasPrefix(part, "state=") {
			state = strings.TrimPrefix(part, "state=")
		}
	}
	second, _, err := svc.Callback(t.Context(), state, "stub-code", nil, "ua", "127.0.0.1")
	if err != nil {
		t.Fatalf("repeat callback: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("repeat login created a second account")
	}
}
