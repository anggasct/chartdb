package diagram

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/anggasct/chartdb/server/apierror"
)

const (
	DefaultListLimit = 50
	MaxListLimit     = 200
)

type Diagram struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Document  any       `json:"document"`
	Version   int       `json:"head_version"`
	Epoch     int       `json:"epoch"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Summary struct {
	ID        uuid.UUID `json:"id"`
	Title     string    `json:"title"`
	Version   int       `json:"head_version"`
	Epoch     int       `json:"epoch"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Version struct {
	Version   int       `json:"version"`
	Document  any       `json:"document,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type Service struct {
	DB          *sql.DB
	MaxDocBytes int64
}

func NewService(db *sql.DB, maxDocBytes int64) *Service {
	return &Service{DB: db, MaxDocBytes: maxDocBytes}
}

func canonical(raw []byte) ([]byte, error) {
	var v any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, errors.New("unexpected data after JSON document")
	}
	return json.Marshal(v)
}

func parseID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, apierror.BadRequest("invalid_id", "diagram id is not a valid UUID")
	}
	return parsed, nil
}

func (s *Service) Create(ctx context.Context, owner uuid.UUID, title string, raw []byte) (Summary, error) {
	doc, err := canonical(raw)
	if err != nil {
		return Summary{}, apierror.BadRequest("invalid_document", "diagram body must be a single JSON document")
	}
	if title == "" {
		title = "Untitled diagram"
	}
	id := uuid.New()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, apierror.Internal()
	}
	defer tx.Rollback()
	var createdAt, updatedAt time.Time
	var epoch int
	if err := tx.QueryRowContext(ctx, `INSERT INTO diagram (id, owner_account_id, title, document, head_version) VALUES ($1, $2, $3, $4::jsonb, 1) RETURNING created_at, updated_at, epoch`, id, owner, title, string(doc)).Scan(&createdAt, &updatedAt, &epoch); err != nil {
		return Summary{}, apierror.Internal()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO diagram_version (id, diagram_id, version, document, created_by) VALUES ($1, $2, 1, $3::jsonb, $4)`, uuid.New(), id, string(doc), owner.String()); err != nil {
		return Summary{}, apierror.Internal()
	}
	if err := tx.Commit(); err != nil {
		return Summary{}, apierror.Internal()
	}
	return Summary{ID: id, Title: title, Version: 1, Epoch: epoch, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func (s *Service) Get(ctx context.Context, owner uuid.UUID, id string) (Diagram, error) {
	diagramID, err := parseID(id)
	if err != nil {
		return Diagram{}, err
	}
	var d Diagram
	var ownerID uuid.UUID
	var doc string
	err = s.DB.QueryRowContext(ctx, `SELECT id, owner_account_id, title, document::text, head_version, epoch, created_at, updated_at FROM diagram WHERE id = $1`, diagramID).Scan(&d.ID, &ownerID, &d.Title, &doc, &d.Version, &d.Epoch, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return Diagram{}, apierror.NotFound("not_found", "diagram not found")
	}
	if err != nil {
		return Diagram{}, apierror.Internal()
	}
	if ownerID != owner {
		return Diagram{}, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	if err := json.Unmarshal([]byte(doc), &d.Document); err != nil {
		return Diagram{}, apierror.Internal()
	}
	return d, nil
}

func (s *Service) Put(ctx context.Context, owner uuid.UUID, id string, raw []byte) (Summary, error) {
	return s.PutWithForce(ctx, owner, id, raw, false)
}

func (s *Service) PutWithForce(ctx context.Context, owner uuid.UUID, id string, raw []byte, force bool) (Summary, error) {
	diagramID, err := parseID(id)
	if err != nil {
		return Summary{}, err
	}
	doc, err := canonical(raw)
	if err != nil {
		return Summary{}, apierror.BadRequest("invalid_document", "diagram body must be a single JSON document")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, apierror.Internal()
	}
	defer tx.Rollback()
	var ownerID uuid.UUID
	var title string
	var current string
	var head int
	var epoch int
	var createdAt time.Time
	err = tx.QueryRowContext(ctx, `SELECT owner_account_id, title, document::text, head_version, epoch, created_at FROM diagram WHERE id = $1 FOR UPDATE`, diagramID).Scan(&ownerID, &title, &current, &head, &epoch, &createdAt)
	if err == sql.ErrNoRows {
		return Summary{}, apierror.NotFound("not_found", "diagram not found")
	}
	if err != nil {
		return Summary{}, apierror.Internal()
	}
	if ownerID != owner {
		return Summary{}, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	stored, err := canonical([]byte(current))
	if err != nil {
		return Summary{}, apierror.Internal()
	}
	var updatedAt time.Time
	if bytes.Equal(stored, doc) {
		if err := tx.QueryRowContext(ctx, `SELECT updated_at, epoch FROM diagram WHERE id = $1`, diagramID).Scan(&updatedAt, &epoch); err != nil {
			return Summary{}, apierror.Internal()
		}
		if err := tx.Commit(); err != nil {
			return Summary{}, apierror.Internal()
		}
		return Summary{ID: diagramID, Title: title, Version: head, Epoch: epoch, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
	}
	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM diagram_version WHERE diagram_id = $1`, diagramID).Scan(&maxVersion); err != nil {
		return Summary{}, apierror.Internal()
	}
	head = maxVersion + 1
	if head < 1 {
		head = 1
	}
	newEpoch := epoch
	if force {
		newEpoch = epoch + 1
	}
	if err := tx.QueryRowContext(ctx, `UPDATE diagram SET document = $1::jsonb, head_version = $2, epoch = $3, updated_at = now() WHERE id = $4 RETURNING updated_at`, string(doc), head, newEpoch, diagramID).Scan(&updatedAt); err != nil {
		return Summary{}, apierror.Internal()
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO diagram_version (id, diagram_id, version, document, created_by) VALUES ($1, $2, $3, $4::jsonb, $5)`, uuid.New(), diagramID, head, string(doc), owner.String()); err != nil {
		return Summary{}, apierror.Internal()
	}
	if err := tx.Commit(); err != nil {
		return Summary{}, apierror.Internal()
	}
	return Summary{ID: diagramID, Title: title, Version: head, Epoch: newEpoch, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}

func (s *Service) Delete(ctx context.Context, owner uuid.UUID, id string) error {
	diagramID, err := parseID(id)
	if err != nil {
		return err
	}
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
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM diagram WHERE id = $1`, diagramID); err != nil {
		return apierror.Internal()
	}
	return nil
}

func (s *Service) List(ctx context.Context, owner uuid.UUID, limit int, cursor string) ([]Summary, string, error) {
	if limit <= 0 {
		limit = DefaultListLimit
	}
	if limit > MaxListLimit {
		limit = MaxListLimit
	}
	offset := 0
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return nil, "", apierror.BadRequest("invalid_cursor", "cursor is not valid")
		}
		offset, err = strconv.Atoi(string(raw))
		if err != nil || offset < 0 {
			return nil, "", apierror.BadRequest("invalid_cursor", "cursor is not valid")
		}
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, title, head_version, epoch, created_at, updated_at FROM diagram WHERE owner_account_id = $1 ORDER BY updated_at DESC, id DESC LIMIT $2 OFFSET $3`, owner, limit+1, offset)
	if err != nil {
		return nil, "", apierror.Internal()
	}
	defer rows.Close()
	out := []Summary{}
	for rows.Next() {
		var item Summary
		if err := rows.Scan(&item.ID, &item.Title, &item.Version, &item.Epoch, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, "", apierror.Internal()
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", apierror.Internal()
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset + limit)))
	}
	return out, next, nil
}

func owned(ctx context.Context, q *sql.Tx, owner, diagramID uuid.UUID) (bool, bool, error) {
	var ownerID uuid.UUID
	err := q.QueryRowContext(ctx, `SELECT owner_account_id FROM diagram WHERE id = $1`, diagramID).Scan(&ownerID)
	if err == sql.ErrNoRows {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return true, ownerID == owner, nil
}

func (s *Service) Versions(ctx context.Context, owner uuid.UUID, id string) ([]Version, error) {
	diagramID, err := parseID(id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, apierror.Internal()
	}
	defer tx.Rollback()
	found, isOwner, err := owned(ctx, tx, owner, diagramID)
	if err != nil {
		return nil, apierror.Internal()
	}
	if !found {
		return nil, apierror.NotFound("not_found", "diagram not found")
	}
	if !isOwner {
		return nil, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	rows, err := tx.QueryContext(ctx, `SELECT version, created_by, created_at FROM diagram_version WHERE diagram_id = $1 ORDER BY version DESC`, diagramID)
	if err != nil {
		return nil, apierror.Internal()
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		if err := rows.Scan(&v.Version, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, apierror.Internal()
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, apierror.Internal()
	}
	_ = tx.Commit()
	return out, nil
}

func (s *Service) VersionDoc(ctx context.Context, owner uuid.UUID, id string, n int) (Version, error) {
	diagramID, err := parseID(id)
	if err != nil {
		return Version{}, err
	}
	tx, err := s.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Version{}, apierror.Internal()
	}
	defer tx.Rollback()
	found, isOwner, err := owned(ctx, tx, owner, diagramID)
	if err != nil {
		return Version{}, apierror.Internal()
	}
	if !found {
		return Version{}, apierror.NotFound("not_found", "diagram not found")
	}
	if !isOwner {
		return Version{}, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	var v Version
	var doc string
	err = tx.QueryRowContext(ctx, `SELECT version, document::text, created_by, created_at FROM diagram_version WHERE diagram_id = $1 AND version = $2`, diagramID, n).Scan(&v.Version, &doc, &v.CreatedBy, &v.CreatedAt)
	if err == sql.ErrNoRows {
		return Version{}, apierror.NotFound("version_not_found", "version not found")
	}
	if err != nil {
		return Version{}, apierror.Internal()
	}
	if err := json.Unmarshal([]byte(doc), &v.Document); err != nil {
		return Version{}, apierror.Internal()
	}
	_ = tx.Commit()
	return v, nil
}

func (s *Service) Restore(ctx context.Context, owner uuid.UUID, id string, n int) (Summary, error) {
	diagramID, err := parseID(id)
	if err != nil {
		return Summary{}, err
	}
	if n < 1 {
		return Summary{}, apierror.BadRequest("invalid_version", "version must be a positive number")
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Summary{}, apierror.Internal()
	}
	defer tx.Rollback()
	var ownerID uuid.UUID
	if err := tx.QueryRowContext(ctx, `SELECT owner_account_id FROM diagram WHERE id = $1 FOR UPDATE`, diagramID).Scan(&ownerID); err != nil {
		if err == sql.ErrNoRows {
			return Summary{}, apierror.NotFound("not_found", "diagram not found")
		}
		return Summary{}, apierror.Internal()
	}
	if ownerID != owner {
		return Summary{}, apierror.Forbidden("forbidden", "you do not own this diagram")
	}
	var doc string
	if err := tx.QueryRowContext(ctx, `SELECT document::text FROM diagram_version WHERE diagram_id = $1 AND version = $2`, diagramID, n).Scan(&doc); err != nil {
		if err == sql.ErrNoRows {
			return Summary{}, apierror.NotFound("version_not_found", "version not found")
		}
		return Summary{}, apierror.Internal()
	}
	var maxVersion int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM diagram_version WHERE diagram_id = $1`, diagramID).Scan(&maxVersion); err != nil {
		return Summary{}, apierror.Internal()
	}
	next := maxVersion + 1
	if next < 1 {
		next = 1
	}
	var out Summary
	var title string
	var createdAt time.Time
	var epoch int
	if err := tx.QueryRowContext(ctx, `UPDATE diagram SET document = $1::jsonb, head_version = $2, updated_at = now() WHERE id = $3 RETURNING id, title, head_version, epoch, created_at, updated_at`, doc, next, diagramID).Scan(&out.ID, &title, &out.Version, &epoch, &createdAt, &out.UpdatedAt); err != nil {
		return Summary{}, apierror.Internal()
	}
	out.Title = title
	out.Epoch = epoch
	out.CreatedAt = createdAt
	if _, err := tx.ExecContext(ctx, `INSERT INTO diagram_version (id, diagram_id, version, document, created_by) VALUES ($1, $2, $3, $4::jsonb, $5)`, uuid.New(), diagramID, next, doc, owner.String()); err != nil {
		return Summary{}, apierror.Internal()
	}
	if err := tx.Commit(); err != nil {
		return Summary{}, apierror.Internal()
	}
	return out, nil
}
