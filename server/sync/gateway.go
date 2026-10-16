package sync

import (
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	ygsync "github.com/reearth/ygo/sync"
)

func (h *Hub) ServeWS(w http.ResponseWriter, r *http.Request, diagramID uuid.UUID, _ uuid.UUID) {
	ctx := r.Context()

	room, err := h.GetOrLoad(ctx, diagramID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if room.PeerLen() >= h.MaxConnPerDiagram {
		http.Error(w, "too many connections", http.StatusTooManyRequests)
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(h.MaxWSMsgBytes + 1024)

	peer := &Peer{
		id:       newPeerID(),
		conn:     conn,
		lastSeen: time.Now(),
	}
	room.mu.Lock()
	peer.epoch = room.epoch
	room.mu.Unlock()

	if !room.addPeer(peer) {
		_ = conn.Close(websocket.StatusTryAgainLater, "too many connections")
		return
	}
	defer room.removePeer(peer)

	room.mu.Lock()
	epoch := room.epoch
	room.mu.Unlock()
	_ = conn.Write(ctx, websocket.MessageBinary, EncodeControl(ControlEpoch, epoch))
	_ = conn.Write(ctx, websocket.MessageBinary, EncodeSyncEnvelope(epoch, ygsync.EncodeSyncStep1(room.doc)))

	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if int64(len(raw)) > h.MaxWSMsgBytes {
			_ = conn.Close(websocket.StatusPolicyViolation, "message too large")
			return
		}
		env, payload, err := DecodeEnvelope(raw)
		if err != nil {
			continue
		}
		switch env {
		case EnvelopeSync:
			msgEpoch, inner, err := DecodeSyncPayload(payload)
			if err != nil {
				continue
			}
			room.mu.Lock()
			curEpoch := room.epoch
			room.mu.Unlock()
			if msgEpoch != curEpoch {
				_ = conn.Write(ctx, websocket.MessageBinary, EncodeControl(ControlReset, curEpoch))
				continue
			}
			room.mu.Lock()
			peer.epoch = curEpoch
			room.mu.Unlock()
			replyInner, err := room.applyUpdate(peer, msgEpoch, inner)
			if err != nil {
				if err == errStaleEpoch {
					_ = conn.Write(ctx, websocket.MessageBinary, EncodeControl(ControlReset, curEpoch))
				}
				continue
			}
			if len(replyInner) > 0 {
				room.mu.Lock()
				cur := room.epoch
				room.mu.Unlock()
				_ = conn.Write(ctx, websocket.MessageBinary, EncodeSyncEnvelope(cur, replyInner))
			}
		case EnvelopeAwareness:
			if int64(len(payload)) > h.MaxWSMsgBytes {
				_ = conn.Close(websocket.StatusPolicyViolation, "message too large")
				return
			}
			room.handleAwareness(peer, payload)
		case EnvelopeControl:
			ctrl, err := DecodeControl(payload)
			if err != nil {
				continue
			}
			if ctrl.Kind == ControlEpoch {
				room.mu.Lock()
				cur := room.epoch
				peer.epoch = cur
				room.mu.Unlock()
				_ = conn.Write(ctx, websocket.MessageBinary, EncodeControl(ControlEpoch, cur))
			}
		default:
			continue
		}
	}
}
