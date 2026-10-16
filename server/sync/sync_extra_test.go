package sync_test

import (
	"bytes"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/reearth/ygo/crdt"
	ygsync "github.com/reearth/ygo/sync"

	"github.com/anggasct/chartdb/server/sync"
	"github.com/anggasct/chartdb/server/testhelper"
)

func TestBroadcast(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	ca, stopA := dialOwner(t, env, alice, diagramID)
	defer stopA()
	defer ca.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var epoch int
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, ca)
		et, p := mustEnvelope(t, raw)
		if i == 0 {
			if et != sync.EnvelopeControl {
				t.Fatalf("expected control, got %d", et)
			}
			ctrl, _ := sync.DecodeControl(p)
			epoch = ctrl.Epoch
		}
	}

	cb, stopB := dialOwner(t, env, alice, diagramID)
	defer stopB()
	defer cb.CloseNow()
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, cb)
		_, _ = mustEnvelope(t, raw)
	}

	docA := crdt.New()
	docA.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("tables").Set(txn, "t1", "users")
	})
	upd := crdt.EncodeStateAsUpdateV1(docA, nil)
	if len(upd) == 0 {
		t.Fatalf("empty update")
	}
	innerUpd := ygsync.EncodeUpdate(upd)
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	if err := ca.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, innerUpd)); err != nil {
		wcan()
		t.Fatalf("A write update: %v", err)
	}
	wcan()

	deadline := time.Now().Add(8 * time.Second)
	got := false
	var gotBytes []byte
	for time.Now().Before(deadline) && !got {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := cb.Read(rctx)
		rcan()
		if err != nil {
			continue
		}
		et, p := mustEnvelope(t, raw)
		if et != sync.EnvelopeSync {
			continue
		}
		_, inner, err := sync.DecodeSyncPayload(p)
		if err != nil {
			continue
		}
		mt, payload, err := ygsync.ReadSyncMessage(inner)
		if err != nil || mt != ygsync.MsgUpdate {
			continue
		}
		gotBytes = payload
		got = true
	}
	if !got {
		t.Fatalf("B must receive A update via broadcast")
	}
	if !bytes.Equal(gotBytes, upd) {
		t.Fatalf("broadcast bytes must match sent update")
	}
}

func TestPersistenceReal(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)
	parsed, _ := uuid.Parse(diagramID)
	hub := env.API.SyncHub()

	c, stop := dialOwner(t, env, alice, diagramID)
	defer stop()
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var epoch int
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, c)
		et, p := mustEnvelope(t, raw)
		if i == 0 && et == sync.EnvelopeControl {
			ctrl, _ := sync.DecodeControl(p)
			epoch = ctrl.Epoch
		}
	}

	for i := 0; i < 600; i++ {
		single := crdt.New()
		key := "k" + uuid.NewString()[:8]
		single.Transact(func(txn *crdt.Transaction) {
			txn.GetMap("m").Set(txn, key, i)
		})
		inc := crdt.EncodeStateAsUpdateV1(single, nil)
		wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
		inner := ygsync.EncodeUpdate(inc)
		if err := c.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, inner)); err != nil {
			wcan()
			t.Fatalf("write %d: %v", i, err)
		}
		wcan()
		if i%100 == 99 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	time.Sleep(500 * time.Millisecond)

	var nUpdates int
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sync_update WHERE diagram_id = $1`, parsed).Scan(&nUpdates); err != nil {
			t.Fatalf("count updates: %v", err)
		}
		if nUpdates >= 600 || time.Now().After(deadline) {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if nUpdates < 600 {
		t.Fatalf("expected >=600 sync_update rows, got %d", nUpdates)
	}
	var nSnap int
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sync_snapshot WHERE diagram_id = $1`, parsed).Scan(&nSnap); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if nSnap < 1 {
		t.Fatalf("expected >=1 snapshot, got %d", nSnap)
	}
	var atSeq int64
	var snapBytes []byte
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT update, at_seq FROM sync_snapshot WHERE diagram_id = $1`, parsed).Scan(&snapBytes, &atSeq); err != nil {
		t.Fatalf("load snapshot: %v", err)
	}

	hub.Drop(diagramID)
	room2, err := hub.GetOrLoad(context.Background(), parsed)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	rows, err := env.Store.DB.QueryContext(context.Background(), `SELECT update FROM sync_update WHERE diagram_id = $1 AND seq > $2 ORDER BY seq ASC`, parsed, atSeq)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	defer rows.Close()
	replay := crdt.New()
	if len(snapBytes) > 0 {
		_ = crdt.ApplyUpdateV1(replay, snapBytes, nil)
	}
	for rows.Next() {
		var u []byte
		if err := rows.Scan(&u); err != nil {
			t.Fatalf("scan tail: %v", err)
		}
		_ = crdt.ApplyUpdateV1(replay, u, nil)
	}
	_ = crdt.ApplyUpdateV1(replay, snapBytes, nil)
	tailRows, err := env.Store.DB.QueryContext(context.Background(), `SELECT update FROM sync_update WHERE diagram_id = $1 AND seq > $2 ORDER BY seq ASC`, parsed, atSeq)
	if err != nil {
		t.Fatalf("tail2: %v", err)
	}
	defer tailRows.Close()
	for tailRows.Next() {
		var u []byte
		_ = tailRows.Scan(&u)
		_ = crdt.ApplyUpdateV1(replay, u, nil)
	}
	if stateHash(replay) != stateHash(room2.Doc()) {
		t.Fatalf("snapshot+tail reload must match room hash (idempotent double-apply)")
	}
	var maxSeq int64
	_ = env.Store.DB.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(seq),0) FROM sync_update WHERE diagram_id=$1`, parsed).Scan(&maxSeq)
	if atSeq > maxSeq {
		t.Fatalf("snapshot at_seq beyond max seq")
	}
	_ = http.StatusOK
}
