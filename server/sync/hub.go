package sync

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/reearth/ygo/awareness"
	"github.com/reearth/ygo/crdt"
)

type Hub struct {
	DB                *sql.DB
	MaxWSMsgBytes     int64
	MaxConnPerDiagram int
	SnapshotEveryN    int
	AwarenessTimeout  time.Duration

	mu    sync.Mutex
	rooms map[string]*Room
}

func NewHub(db *sql.DB, maxWSMsg int64, maxConn, snapshotEveryN int, awarenessTimeout time.Duration) *Hub {
	if maxWSMsg <= 0 {
		maxWSMsg = 1 << 20
	}
	if maxConn <= 0 {
		maxConn = 50
	}
	if snapshotEveryN <= 0 {
		snapshotEveryN = 500
	}
	if awarenessTimeout <= 0 {
		awarenessTimeout = 30 * time.Second
	}
	return &Hub{
		DB:                db,
		MaxWSMsgBytes:     maxWSMsg,
		MaxConnPerDiagram: maxConn,
		SnapshotEveryN:    snapshotEveryN,
		AwarenessTimeout:  awarenessTimeout,
		rooms:             make(map[string]*Room),
	}
}

func (h *Hub) PeerCount(diagramID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[diagramID]
	if !ok {
		return 0
	}
	return r.PeerLen()
}

func (h *Hub) Drop(diagramID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if r, ok := h.rooms[diagramID]; ok {
		r.destroy()
		delete(h.rooms, diagramID)
	}
}

func (h *Hub) getRoom(diagramID string) (*Room, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	r, ok := h.rooms[diagramID]
	return r, ok
}

func (h *Hub) GetOrLoad(ctx context.Context, diagramID uuid.UUID) (*Room, error) {
	key := diagramID.String()
	h.mu.Lock()
	if r, ok := h.rooms[key]; ok {
		h.mu.Unlock()
		return r, nil
	}
	h.mu.Unlock()

	r, err := h.loadRoom(ctx, diagramID)
	if err != nil {
		return nil, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if existing, ok := h.rooms[key]; ok {
		r.destroy()
		return existing, nil
	}
	h.rooms[key] = r
	return r, nil
}

func (h *Hub) loadRoom(ctx context.Context, diagramID uuid.UUID) (*Room, error) {
	var epoch int
	err := h.DB.QueryRowContext(ctx, `SELECT epoch FROM diagram WHERE id = $1`, diagramID).Scan(&epoch)
	if err == sql.ErrNoRows {
		return nil, err
	}
	if err != nil {
		return nil, err
	}

	doc := crdt.New()
	var atSeq int64
	var snap []byte
	err = h.DB.QueryRowContext(ctx, `SELECT update, at_seq FROM sync_snapshot WHERE diagram_id = $1`, diagramID).Scan(&snap, &atSeq)
	if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	if err == nil && len(snap) > 0 {
		_ = crdt.ApplyUpdateV1(doc, snap, nil)
	}

	rows, err := h.DB.QueryContext(ctx, `SELECT update, seq FROM sync_update WHERE diagram_id = $1 AND seq > $2 ORDER BY seq ASC`, diagramID, atSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var maxSeq = atSeq
	for rows.Next() {
		var upd []byte
		var seq int64
		if err := rows.Scan(&upd, &seq); err != nil {
			return nil, err
		}
		if len(upd) > 0 {
			_ = crdt.ApplyUpdateV1(doc, upd, nil)
		}
		if seq > maxSeq {
			maxSeq = seq
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	r := newRoom(diagramID, doc, epoch, maxSeq, h)
	r.startExpiryLoop()
	return r, nil
}

func (h *Hub) ForceReset(ctx context.Context, diagramID uuid.UUID, base []byte) (int, error) {
	var epoch int
	err := h.DB.QueryRowContext(ctx, `UPDATE diagram SET epoch = epoch + 1, updated_at = now() WHERE id = $1 RETURNING epoch`, diagramID).Scan(&epoch)
	if err != nil {
		return 0, err
	}

	fresh := crdt.New()
	if len(base) > 0 {
		_ = crdt.ApplyUpdateV1(fresh, base, nil)
	}
	state := crdt.EncodeStateAsUpdateV1(fresh, nil)

	var maxSeq int64
	_ = h.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM sync_update WHERE diagram_id = $1`, diagramID).Scan(&maxSeq)
	_, _ = h.DB.ExecContext(ctx, `INSERT INTO sync_snapshot (diagram_id, update, at_seq) VALUES ($1, $2, $3) ON CONFLICT (diagram_id) DO UPDATE SET update = EXCLUDED.update, at_seq = EXCLUDED.at_seq, created_at = now()`, diagramID, state, maxSeq)

	key := diagramID.String()
	h.mu.Lock()
	r, ok := h.rooms[key]
	h.mu.Unlock()
	if ok {
		r.resetTo(fresh, epoch, state, maxSeq)
	}
	return epoch, nil
}

type Room struct {
	diagramID uuid.UUID
	doc       *crdt.Doc
	epoch     int
	lastSeq   int64
	sinceSnap int

	hub *Hub

	mu    sync.Mutex
	peers map[string]*Peer
	aware *awareness.Awareness
	stop  func()
}

func newRoom(diagramID uuid.UUID, doc *crdt.Doc, epoch int, lastSeq int64, hub *Hub) *Room {
	return &Room{
		diagramID: diagramID,
		doc:       doc,
		epoch:     epoch,
		lastSeq:   lastSeq,
		hub:       hub,
		peers:     make(map[string]*Peer),
		aware:     awareness.New(0),
	}
}

func (r *Room) startExpiryLoop() {
	r.aware.StartAutoExpiry(r.hub.AwarenessTimeout)
	unsub := r.aware.OnChange(func(ev awareness.ChangeEvent) {
		if len(ev.Removed) == 0 {
			return
		}
		payload := r.aware.EncodeUpdate(ev.Removed)
		if len(payload) == 0 {
			return
		}
		r.broadcastAwareness(payload, nil)
	})
	r.stop = unsub
}

func (r *Room) destroy() {
	if r.stop != nil {
		r.stop()
	}
	r.aware.Destroy()
}

func (r *Room) PeerLen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.peers)
}

func (r *Room) Epoch() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.epoch
}

func (r *Room) Doc() *crdt.Doc {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.doc
}

func (r *Room) StateHash() []byte {
	return crdt.EncodeStateAsUpdateV1(r.doc, nil)
}

func (r *Room) resetTo(fresh *crdt.Doc, epoch int, _ []byte, lastSeq int64) {
	r.mu.Lock()
	r.doc = fresh
	r.epoch = epoch
	r.lastSeq = lastSeq
	r.sinceSnap = 0
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		p.epoch = epoch
		peers = append(peers, p)
	}
	r.mu.Unlock()
	msg := EncodeControl(ControlReset, epoch)
	for _, p := range peers {
		p.send(msg)
	}
}

func (r *Room) ResetForForce(state []byte, epoch int, lastSeq int64) {
	fresh := crdt.New()
	if len(state) > 0 {
		_ = crdt.ApplyUpdateV1(fresh, state, nil)
	}
	r.resetTo(fresh, epoch, state, lastSeq)
}
