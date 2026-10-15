package share

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/apierror"
)

type Link struct {
	ID        uuid.UUID  `json:"id"`
	ExpiresAt *time.Time `json:"expires_at"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

type Created struct {
	Link
	Token string `json:"token"`
}

type SharedDocument struct {
	Title    string `json:"title"`
	Document any    `json:"document"`
	Version  int    `json:"version"`
}

type Service struct {
	DB *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{DB: db}
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func (s *Service) Create(ctx context.Context, owner, diagramID uuid.UUID, expiresAt *time.Time) (Created, error) {
	var ownerID uuid.UUID
	if err := s.DB.QueryRowContext(ctx, `SELECT owner_account_id FROM diagram WHERE id = $1`, diagramID).Scan(&ownerID); err != nil {
		if err == sql.ErrNoRows {
			return Created{}, apierror.NotFound("not_found", "diagram not found")
		}
		return Created{}, apierror.Internal()
	}
	if ownerID != owner {
		return Created{}, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Created{}, apierror.Internal()
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	id := uuid.New()
	var out Created
	var exp, rev any
	if expiresAt != nil {
		exp = *expiresAt
		out.ExpiresAt = expiresAt
	}
	if err := s.DB.QueryRowContext(ctx, `INSERT INTO share_link (id, diagram_id, token_hash, created_by, expires_at) VALUES ($1, $2, $3, $4, $5) RETURNING created_at, revoked_at`, id, diagramID, hashToken(token), owner.String(), exp).Scan(&out.CreatedAt, &rev); err != nil {
		return Created{}, apierror.Internal()
	}
	out.ID = id
	out.Token = token
	return out, nil
}

func (s *Service) List(ctx context.Context, owner, diagramID uuid.UUID) ([]Link, error) {
	var ownerID uuid.UUID
	if err := s.DB.QueryRowContext(ctx, `SELECT owner_account_id FROM diagram WHERE id = $1`, diagramID).Scan(&ownerID); err != nil {
		if err == sql.ErrNoRows {
			return nil, apierror.NotFound("not_found", "diagram not found")
		}
		return nil, apierror.Internal()
	}
	if ownerID != owner {
		return nil, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, expires_at, revoked_at, created_at FROM share_link WHERE diagram_id = $1 ORDER BY created_at DESC`, diagramID)
	if err != nil {
		return nil, apierror.Internal()
	}
	defer rows.Close()
	out := []Link{}
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.ExpiresAt, &l.RevokedAt, &l.CreatedAt); err != nil {
			return nil, apierror.Internal()
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, apierror.Internal()
	}
	return out, nil
}

func (s *Service) Revoke(ctx context.Context, owner, diagramID, shareID uuid.UUID) error {
	var ownerID uuid.UUID
	if err := s.DB.QueryRowContext(ctx, `SELECT owner_account_id FROM diagram WHERE id = $1`, diagramID).Scan(&ownerID); err != nil {
		if err == sql.ErrNoRows {
			return apierror.NotFound("not_found", "diagram not found")
		}
		return apierror.Internal()
	}
	if ownerID != owner {
		return apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE share_link SET revoked_at = now() WHERE id = $1 AND diagram_id = $2`, shareID, diagramID)
	if err != nil {
		return apierror.Internal()
	}
	n, err := res.RowsAffected()
	if err != nil {
		return apierror.Internal()
	}
	if n == 0 {
		return apierror.NotFound("share_not_found", "share link not found")
	}
	return nil
}

var errShareNotFound = apierror.NotFound("not_found", "shared diagram not found")

func (s *Service) ReadPublic(ctx context.Context, token string) (SharedDocument, error) {
	var out SharedDocument
	var doc string
	err := s.DB.QueryRowContext(ctx, `SELECT d.title, d.document::text, d.head_version FROM share_link l JOIN diagram d ON d.id = l.diagram_id WHERE l.token_hash = $1 AND l.revoked_at IS NULL AND (l.expires_at IS NULL OR l.expires_at > now())`, hashToken(token)).Scan(&out.Title, &doc, &out.Version)
	if err == sql.ErrNoRows {
		return SharedDocument{}, errShareNotFound
	}
	if err != nil {
		return SharedDocument{}, apierror.Internal()
	}
	if err := json.Unmarshal([]byte(doc), &out.Document); err != nil {
		return SharedDocument{}, apierror.Internal()
	}
	return out, nil
}
