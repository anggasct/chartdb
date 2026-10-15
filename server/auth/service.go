package auth

import (
	"context"
	"database/sql"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/apierror"
	"github.com/anggasct/chartdb/server/session"
)

type Account struct {
	ID           uuid.UUID `json:"id"`
	DisplayName  string    `json:"display_name"`
	PrimaryEmail *string   `json:"primary_email"`
}

type Identity struct {
	Provider      string  `json:"provider"`
	Email         *string `json:"email"`
	EmailVerified bool    `json:"email_verified"`
}

type Service struct {
	DB              *sql.DB
	SessionLifetime time.Duration
	ArgonMemory     uint32
	ArgonTime       uint32
	ArgonThreads    uint8
	Providers       map[string]Provider
	states          *stateStore
}

func NewService(db *sql.DB, lifetime time.Duration, mem, t uint32, threads uint8, providers map[string]Provider) *Service {
	if providers == nil {
		providers = map[string]Provider{}
	}
	return &Service{DB: db, SessionLifetime: lifetime, ArgonMemory: mem, ArgonTime: t, ArgonThreads: threads, Providers: providers, states: newStateStore(10 * time.Minute)}
}

func normalizeEmail(email string) (string, *apierror.Error) {
	trimmed := strings.ToLower(strings.TrimSpace(email))
	addr, err := mail.ParseAddress(trimmed)
	if err != nil || !strings.Contains(addr.Address, "@") {
		return "", apierror.BadRequest("invalid_email", "enter a valid email address")
	}
	return strings.ToLower(addr.Address), nil
}

func defaultDisplayName(email string) string {
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i]
	}
	return email
}

func (s *Service) Register(ctx context.Context, email, password, displayName, userAgent, ip string) (Account, string, error) {
	addr, apiErr := normalizeEmail(email)
	if apiErr != nil {
		return Account{}, "", apiErr
	}
	if len(password) < 8 || len(password) > 1024 {
		return Account{}, "", apierror.BadRequest("weak_password", "password must be at least 8 characters")
	}
	if displayName == "" {
		displayName = defaultDisplayName(addr)
	}
	hash, err := HashPassword(password, s.ArgonMemory, s.ArgonTime, s.ArgonThreads)
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM identity WHERE provider = 'local' AND subject = $1)`, addr).Scan(&exists); err != nil {
		return Account{}, "", apierror.Internal()
	}
	if exists {
		return Account{}, "", apierror.Conflict("email_taken", "an account with this email already exists")
	}
	accountID := uuid.New()
	identityID := uuid.New()
	if _, err := tx.ExecContext(ctx, `INSERT INTO account (id, display_name, primary_email) VALUES ($1, $2, $3)`, accountID, displayName, addr); err != nil {
		return Account{}, "", apierror.Internal()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO identity (id, account_id, provider, subject, email, email_verified) VALUES ($1, $2, 'local', $3, $3, false)`, identityID, accountID, addr); err != nil {
		return Account{}, "", apierror.Internal()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO local_credential (identity_id, password_hash) VALUES ($1, $2)`, identityID, hash); err != nil {
		return Account{}, "", apierror.Internal()
	}
	token, _, err := session.Create(ctx, tx, accountID, userAgent, ip, s.SessionLifetime)
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	if err := tx.Commit(); err != nil {
		return Account{}, "", apierror.Internal()
	}
	emailCopy := addr
	return Account{ID: accountID, DisplayName: displayName, PrimaryEmail: &emailCopy}, token, nil
}

var invalidCredentials = apierror.Unauthorized("invalid_credentials", "invalid email or password")

func (s *Service) Login(ctx context.Context, email, password, userAgent, ip string) (Account, string, error) {
	addr, apiErr := normalizeEmail(email)
	if apiErr != nil {
		return Account{}, "", invalidCredentials
	}
	var accountID uuid.UUID
	var displayName string
	var primaryEmail *string
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT a.id, a.display_name, a.primary_email, c.password_hash FROM identity i JOIN account a ON a.id = i.account_id JOIN local_credential c ON c.identity_id = i.id WHERE i.provider = 'local' AND i.subject = $1`, addr).Scan(&accountID, &displayName, &primaryEmail, &hash)
	if err == sql.ErrNoRows {
		_, _ = HashPassword(password, s.ArgonMemory, s.ArgonTime, s.ArgonThreads)
		return Account{}, "", invalidCredentials
	}
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	ok, err := VerifyPassword(hash, password)
	if err != nil || !ok {
		return Account{}, "", invalidCredentials
	}
	token, _, err := session.Create(ctx, s.DB, accountID, userAgent, ip, s.SessionLifetime)
	if err != nil {
		return Account{}, "", apierror.Internal()
	}
	return Account{ID: accountID, DisplayName: displayName, PrimaryEmail: primaryEmail}, token, nil
}

func (s *Service) Me(ctx context.Context, accountID uuid.UUID) (Account, []Identity, error) {
	var acc Account
	var id uuid.UUID
	if err := s.DB.QueryRowContext(ctx, `SELECT id, display_name, primary_email FROM account WHERE id = $1`, accountID).Scan(&id, &acc.DisplayName, &acc.PrimaryEmail); err != nil {
		if err == sql.ErrNoRows {
			return Account{}, nil, apierror.Unauthorized("unauthorized", "session is no longer valid")
		}
		return Account{}, nil, apierror.Internal()
	}
	acc.ID = id
	rows, err := s.DB.QueryContext(ctx, `SELECT provider, email, email_verified FROM identity WHERE account_id = $1 ORDER BY provider`, accountID)
	if err != nil {
		return Account{}, nil, apierror.Internal()
	}
	defer rows.Close()
	identities := []Identity{}
	for rows.Next() {
		var ident Identity
		if err := rows.Scan(&ident.Provider, &ident.Email, &ident.EmailVerified); err != nil {
			return Account{}, nil, apierror.Internal()
		}
		identities = append(identities, ident)
	}
	if err := rows.Err(); err != nil {
		return Account{}, nil, apierror.Internal()
	}
	return acc, identities, nil
}

func (s *Service) ChangePassword(ctx context.Context, accountID uuid.UUID, current, next string) error {
	if len(next) < 8 || len(next) > 1024 {
		return apierror.BadRequest("weak_password", "password must be at least 8 characters")
	}
	var identityID uuid.UUID
	var hash string
	err := s.DB.QueryRowContext(ctx, `SELECT i.id, c.password_hash FROM identity i JOIN local_credential c ON c.identity_id = i.id WHERE i.account_id = $1 AND i.provider = 'local'`, accountID).Scan(&identityID, &hash)
	if err == sql.ErrNoRows {
		return apierror.BadRequest("no_local_login", "this account has no local password")
	}
	if err != nil {
		return apierror.Internal()
	}
	ok, err := VerifyPassword(hash, current)
	if err != nil || !ok {
		return apierror.Unauthorized("invalid_credentials", "current password is incorrect")
	}
	updated, err := HashPassword(next, s.ArgonMemory, s.ArgonTime, s.ArgonThreads)
	if err != nil {
		return apierror.Internal()
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE local_credential SET password_hash = $1, updated_at = now() WHERE identity_id = $2`, updated, identityID); err != nil {
		return apierror.Internal()
	}
	return nil
}
