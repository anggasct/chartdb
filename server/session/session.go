package session

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const CookieName = "__Host-chartdb_session"

type Session struct {
	ID        uuid.UUID
	AccountID uuid.UUID
	UserAgent *string
	IP        *string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type Info struct {
	ID        uuid.UUID `json:"id"`
	UserAgent *string   `json:"user_agent"`
	IP        *string   `json:"ip"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func Create(ctx context.Context, q Execer, accountID uuid.UUID, userAgent, ip string, lifetime time.Duration) (string, Session, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", Session{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	id := uuid.New()
	now := time.Now()
	sess := Session{ID: id, AccountID: accountID, CreatedAt: now, ExpiresAt: now.Add(lifetime)}
	var ua, addr any
	if userAgent != "" {
		ua = userAgent
		sess.UserAgent = &userAgent
	}
	if ip != "" {
		addr = ip
		ipCopy := ip
		sess.IP = &ipCopy
	}
	_, err := q.ExecContext(ctx, `INSERT INTO session (id, account_id, token_hash, user_agent, ip, expires_at) VALUES ($1, $2, $3, $4, $5, $6)`, id, accountID, hashToken(token), ua, addr, sess.ExpiresAt)
	if err != nil {
		return "", Session{}, err
	}
	return token, sess, nil
}

func Validate(ctx context.Context, db *sql.DB, raw string) (Session, error) {
	var s Session
	var id uuid.UUID
	var accountID uuid.UUID
	err := db.QueryRowContext(ctx, `SELECT id, account_id, user_agent, ip, created_at, expires_at FROM session WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, hashToken(raw)).Scan(&id, &accountID, &s.UserAgent, &s.IP, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		return Session{}, err
	}
	s.ID = id
	s.AccountID = accountID
	return s, nil
}

func Revoke(ctx context.Context, db *sql.DB, id, accountID uuid.UUID) error {
	res, err := db.ExecContext(ctx, `UPDATE session SET revoked_at = now() WHERE id = $1 AND account_id = $2 AND revoked_at IS NULL`, id, accountID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func RevokeByToken(ctx context.Context, db *sql.DB, raw string) error {
	_, err := db.ExecContext(ctx, `UPDATE session SET revoked_at = now() WHERE token_hash = $1 AND revoked_at IS NULL`, hashToken(raw))
	return err
}

func List(ctx context.Context, db *sql.DB, accountID uuid.UUID) ([]Info, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, user_agent, ip, created_at, expires_at FROM session WHERE account_id = $1 AND revoked_at IS NULL AND expires_at > now() ORDER BY created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Info{}
	for rows.Next() {
		var info Info
		if err := rows.Scan(&info.ID, &info.UserAgent, &info.IP, &info.CreatedAt, &info.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func SetCookie(w http.ResponseWriter, raw string, secure bool, lifetime time.Duration) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    raw,
		Path:     "/",
		MaxAge:   int(lifetime.Seconds()),
		Expires:  time.Now().Add(lifetime),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func ClearCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}
