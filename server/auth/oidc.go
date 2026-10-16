package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/anggasct/chartdb/server/apierror"
	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/session"
)

type IdentityToken struct {
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Nonce         string
}

type Provider interface {
	AuthCodeURL(state, nonce, verifier string) string
	Exchange(ctx context.Context, code, verifier, expectedNonce string) (IdentityToken, error)
}

type pendingLogin struct {
	providerID string
	verifier   string
	nonce      string
	accountID  *uuid.UUID
	expiresAt  time.Time
}

type stateStore struct {
	mu      sync.Mutex
	pending map[string]pendingLogin
	ttl     time.Duration
}

func newStateStore(ttl time.Duration) *stateStore {
	return &stateStore{pending: map[string]pendingLogin{}, ttl: ttl}
}

func (s *stateStore) put(providerID, verifier, nonce string, accountID *uuid.UUID) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	state := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	var bound *uuid.UUID
	if accountID != nil {
		copied := *accountID
		bound = &copied
	}
	s.pending[state] = pendingLogin{providerID: providerID, verifier: verifier, nonce: nonce, accountID: bound, expiresAt: time.Now().Add(s.ttl)}
	return state, nil
}

func (s *stateStore) take(state string) (pendingLogin, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pending[state]
	delete(s.pending, state)
	if !ok || time.Now().After(p.expiresAt) {
		return pendingLogin{}, false
	}
	return p, true
}

type goOIDCProvider struct {
	provider *oidc.Provider
	oauth    oauth2.Config
}

func NewGOIDCProvider(ctx context.Context, cfg config.OIDCProvider) (Provider, error) {
	provider, err := oidc.NewProvider(ctx, cfg.Issuer)
	if err != nil {
		return nil, err
	}
	return &goOIDCProvider{
		provider: provider,
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     provider.Endpoint(),
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
	}, nil
}

func (p *goOIDCProvider) AuthCodeURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce))
}

func (p *goOIDCProvider) Exchange(ctx context.Context, code, verifier, expectedNonce string) (IdentityToken, error) {
	token, err := p.oauth.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return IdentityToken{}, err
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok {
		return IdentityToken{}, apierror.BadRequest("oidc_failed", "provider did not return an identity token")
	}
	idVerifier := p.provider.Verifier(&oidc.Config{ClientID: p.oauth.ClientID})
	idToken, err := idVerifier.Verify(ctx, raw)
	if err != nil {
		return IdentityToken{}, apierror.BadRequest("oidc_failed", "could not verify provider identity")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified *bool  `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return IdentityToken{}, apierror.BadRequest("oidc_failed", "could not read provider identity")
	}
	if expectedNonce == "" || claims.Nonce != expectedNonce {
		return IdentityToken{}, apierror.BadRequest("oidc_failed", "provider login failed")
	}
	verified := false
	if claims.EmailVerified != nil {
		verified = *claims.EmailVerified
	}
	return IdentityToken{Subject: idToken.Subject, Email: strings.ToLower(strings.TrimSpace(claims.Email)), EmailVerified: verified, Name: claims.Name, Nonce: claims.Nonce}, nil
}

func (s *Service) BeginLogin(providerID string, current *uuid.UUID) (string, error) {
	provider, ok := s.Providers[providerID]
	if !ok {
		return "", apierror.NotFound("provider_unknown", "unknown login provider")
	}
	verifier := oauth2.GenerateVerifier()
	nonceRaw := make([]byte, 16)
	if _, err := rand.Read(nonceRaw); err != nil {
		return "", apierror.Internal()
	}
	nonce := base64.RawURLEncoding.EncodeToString(nonceRaw)
	state, err := s.states.put(providerID, verifier, nonce, current)
	if err != nil {
		return "", apierror.Internal()
	}
	return provider.AuthCodeURL(state, nonce, verifier), nil
}

func boundAccountMatches(pending *uuid.UUID, current *uuid.UUID) bool {
	if pending == nil || current == nil {
		return pending == nil && current == nil
	}
	return *pending == *current
}

func (s *Service) Callback(ctx context.Context, state, code string, current *uuid.UUID, userAgent, ip string) (Account, string, error) {
	pending, ok := s.states.take(state)
	if !ok {
		return Account{}, "", apierror.BadRequest("invalid_state", "login session expired, try again")
	}
	if !boundAccountMatches(pending.accountID, current) {
		return Account{}, "", apierror.BadRequest("invalid_state", "login session expired, try again")
	}
	provider, ok := s.Providers[pending.providerID]
	if !ok {
		return Account{}, "", apierror.BadRequest("provider_unknown", "unknown login provider")
	}
	claims, err := provider.Exchange(ctx, code, pending.verifier, pending.nonce)
	if err != nil {
		if apiErr, ok := err.(*apierror.Error); ok {
			return Account{}, "", apiErr
		}
		return Account{}, "", apierror.BadRequest("oidc_failed", "provider login failed")
	}
	if pending.nonce == "" || claims.Nonce != pending.nonce {
		return Account{}, "", apierror.BadRequest("oidc_failed", "provider login failed")
	}
	if claims.Subject == "" {
		return Account{}, "", apierror.BadRequest("oidc_failed", "provider login failed")
	}
	var accountID uuid.UUID
	err = s.DB.QueryRowContext(ctx, `SELECT account_id FROM identity WHERE provider = $1 AND subject = $2`, pending.providerID, claims.Subject).Scan(&accountID)
	if err != nil && err != sql.ErrNoRows {
		return Account{}, "", apierror.Internal()
	}
	if err == sql.ErrNoRows {
		accountID, err = s.linkOrCreate(ctx, pending.providerID, claims, current)
		if err != nil {
			return Account{}, "", err
		}
	}
	token, _, err := session.Create(ctx, s.DB, accountID, userAgent, ip, s.SessionLifetime)
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	acc, _, err := s.Me(ctx, accountID)
	if err != nil {
		return Account{}, "", err
	}
	return acc, token, nil
}

func (s *Service) linkOrCreate(ctx context.Context, providerID string, claims IdentityToken, current *uuid.UUID) (uuid.UUID, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return uuid.Nil, apierror.Internal()
	}
	defer tx.Rollback()
	target := uuid.Nil
	if current != nil {
		target = *current
	} else if claims.Email != "" && claims.EmailVerified {
		_ = tx.QueryRowContext(ctx, `SELECT account_id FROM identity WHERE provider = 'local' AND email = $1 LIMIT 1`, claims.Email).Scan(&target)
	}
	if target == uuid.Nil {
		target = uuid.New()
		displayName := claims.Name
		if displayName == "" {
			displayName = defaultDisplayName(claims.Email)
		}
		if displayName == "" {
			displayName = claims.Subject
		}
		var primaryEmail any
		if claims.Email != "" && claims.EmailVerified {
			primaryEmail = claims.Email
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO account (id, display_name, primary_email) VALUES ($1, $2, $3)`, target, displayName, primaryEmail); err != nil {
			return uuid.Nil, apierror.Internal()
		}
	} else if claims.Email != "" && claims.EmailVerified {
		_, _ = tx.ExecContext(ctx, `UPDATE account SET primary_email = COALESCE(primary_email, $1) WHERE id = $2`, claims.Email, target)
	}
	var email any
	if claims.Email != "" {
		email = claims.Email
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO identity (id, account_id, provider, subject, email, email_verified) VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (provider, subject) DO NOTHING`, uuid.New(), target, providerID, claims.Subject, email, claims.EmailVerified)
	if err != nil {
		return uuid.Nil, apierror.Internal()
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var existing uuid.UUID
		if err := tx.QueryRowContext(ctx, `SELECT account_id FROM identity WHERE provider = $1 AND subject = $2`, providerID, claims.Subject).Scan(&existing); err != nil {
			return uuid.Nil, apierror.Internal()
		}
		target = existing
	}
	if err := tx.Commit(); err != nil {
		return uuid.Nil, apierror.Internal()
	}
	return target, nil
}
