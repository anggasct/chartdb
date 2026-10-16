package sync

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/coder/websocket"
	"github.com/reearth/ygo/crdt"
	"github.com/reearth/ygo/encoding"
	ygsync "github.com/reearth/ygo/sync"
)

var errStaleEpoch = errors.New("stale epoch")

type Peer struct {
	id       string
	epoch    int
	conn     *websocket.Conn
	awIDs    []uint64
	lastSeen time.Time
}

func newPeerID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func (r *Room) addPeer(p *Peer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.peers) >= r.hub.MaxConnPerDiagram {
		return false
	}
	r.peers[p.id] = p
	return true
}

func (r *Room) removePeer(p *Peer) {
	r.mu.Lock()
	_, ok := r.peers[p.id]
	if ok {
		delete(r.peers, p.id)
	}
	empty := len(r.peers) == 0
	r.mu.Unlock()

	if len(p.awIDs) > 0 {
		for _, id := range p.awIDs {
			r.removeAwareness(id)
		}
	}

	if empty {
		_ = r.snapshotIfDirty(context.Background())
	}
}

func (p *Peer) send(msg []byte) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.conn.Write(ctx, websocket.MessageBinary, msg); err != nil {
		return false
	}
	return true
}

func (r *Room) broadcastSync(inner []byte, except *Peer) {
	r.mu.Lock()
	epoch := r.epoch
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		if except != nil && p.id == except.id {
			continue
		}
		peers = append(peers, p)
	}
	r.mu.Unlock()
	msg := EncodeSyncEnvelope(epoch, inner)
	for _, p := range peers {
		p.send(msg)
	}
}

func (r *Room) broadcastAwareness(payload []byte, except *Peer) {
	r.mu.Lock()
	peers := make([]*Peer, 0, len(r.peers))
	for _, p := range r.peers {
		if except != nil && p.id == except.id {
			continue
		}
		peers = append(peers, p)
	}
	r.mu.Unlock()
	msg := EncodeAwarenessEnvelope(payload)
	for _, p := range peers {
		p.send(msg)
	}
}

func parseAwarenessIDs(payload []byte) []uint64 {
	dec := encoding.NewDecoder(payload)
	n, err := dec.ReadVarUint()
	if err != nil {
		return nil
	}
	if n > 100000 {
		return nil
	}
	var out []uint64
	for i := uint64(0); i < n; i++ {
		id, err := dec.ReadVarUint()
		if err != nil {
			return out
		}
		if _, err := dec.ReadVarUint(); err != nil {
			return out
		}
		if _, err := dec.ReadVarBytes(); err != nil {
			return out
		}
		out = append(out, id)
	}
	return out
}

func (r *Room) handleAwareness(from *Peer, payload []byte) {
	if len(from.awIDs) == 0 {
		if ids := parseAwarenessIDs(payload); len(ids) > 0 {
			r.mu.Lock()
			from.awIDs = ids
			from.lastSeen = time.Now()
			r.mu.Unlock()
		}
	} else {
		r.mu.Lock()
		from.lastSeen = time.Now()
		r.mu.Unlock()
	}
	if err := r.aware.ApplyUpdate(payload, from.id); err != nil {
		return
	}
	r.broadcastAwareness(payload, from)
}

func (r *Room) removeAwareness(clientID uint64) {
	states := r.aware.GetStates()
	cs, ok := states[clientID]
	if !ok || cs.State == nil {
		return
	}
	enc := encoding.NewEncoder()
	enc.WriteVarUint(1)
	enc.WriteVarUint(clientID)
	enc.WriteVarUint(cs.Clock + 1)
	enc.WriteVarString("null")
	payload := enc.Bytes()
	_ = r.aware.ApplyUpdate(payload, "leave")
	r.broadcastAwareness(payload, nil)
}

func (r *Room) applyUpdate(from *Peer, msgEpoch int, inner []byte) ([]byte, error) {
	msgType, payload, err := ygsync.ReadSyncMessage(inner)
	if err != nil {
		return nil, err
	}
	switch msgType {
	case ygsync.MsgSyncStep1:
		r.mu.Lock()
		defer r.mu.Unlock()
		return ygsync.EncodeSyncStep2(r.doc, inner)
	case ygsync.MsgSyncStep2, ygsync.MsgUpdate:
		if len(payload) == 0 {
			return nil, nil
		}
		r.mu.Lock()
		if msgEpoch != r.epoch {
			r.mu.Unlock()
			return nil, errStaleEpoch
		}
		if err := crdt.ApplyUpdateV1(r.doc, payload, from.id); err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.mu.Unlock()
		var seq int64
		err = r.hub.DB.QueryRowContext(context.Background(), `INSERT INTO sync_update (diagram_id, update) VALUES ($1, $2) RETURNING seq`, r.diagramID, payload).Scan(&seq)
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		if seq > r.lastSeq {
			r.lastSeq = seq
		}
		r.sinceSnap++
		needSnap := r.sinceSnap >= r.hub.SnapshotEveryN
		r.mu.Unlock()
		out := ygsync.EncodeUpdate(payload)
		r.broadcastSync(out, from)
		if needSnap {
			_ = r.snapshotLocked(context.Background())
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func (r *Room) snapshotIfDirty(ctx context.Context) error {
	r.mu.Lock()
	dirty := r.sinceSnap > 0
	r.mu.Unlock()
	if !dirty {
		return nil
	}
	return r.snapshotLocked(ctx)
}

func (r *Room) snapshotLocked(ctx context.Context) error {
	r.mu.Lock()
	state := crdt.EncodeStateAsUpdateV1(r.doc, nil)
	at := r.lastSeq
	r.mu.Unlock()
	_, err := r.hub.DB.ExecContext(ctx, `INSERT INTO sync_snapshot (diagram_id, update, at_seq) VALUES ($1, $2, $3) ON CONFLICT (diagram_id) DO UPDATE SET update = EXCLUDED.update, at_seq = EXCLUDED.at_seq, created_at = now()`, r.diagramID, state, at)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.lastSeq == at {
		r.sinceSnap = 0
	}
	r.mu.Unlock()
	return nil
}
