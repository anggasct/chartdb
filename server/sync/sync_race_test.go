package sync_test

import (
	"context"
	stdsync "sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/reearth/ygo/crdt"
	ygsync "github.com/reearth/ygo/sync"

	"github.com/anggasct/chartdb/server/config"
	"github.com/anggasct/chartdb/server/sync"
	"github.com/anggasct/chartdb/server/testhelper"
)

func countUpdates(t *testing.T, env *testhelper.Env, diagramID string) int {
	t.Helper()
	parsed, _ := uuid.Parse(diagramID)
	var n int
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sync_update WHERE diagram_id = $1`, parsed).Scan(&n); err != nil {
		t.Fatalf("count updates: %v", err)
	}
	return n
}

func TestSingleUpdateSingleRow(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	c, stop := dialOwner(t, env, alice, diagramID)
	defer stop()
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
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

	single := crdt.New()
	single.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("m").Set(txn, "only", 1)
	})
	inc := crdt.EncodeStateAsUpdateV1(single, nil)
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	if err := c.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, ygsync.EncodeUpdate(inc))); err != nil {
		wcan()
		t.Fatalf("write update: %v", err)
	}
	wcan()

	deadline := time.Now().Add(10 * time.Second)
	for {
		if n := countUpdates(t, env, diagramID); n == 1 {
			break
		} else if n > 1 {
			t.Fatalf("one client update must yield exactly one row, got %d", n)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for single persisted row")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestConcurrentSnapshotHandshakeNoRace(t *testing.T) {
	env := testhelper.Boot(t, func(c *config.Config) { c.SnapshotEveryN = 10 })
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)
	parsed, _ := uuid.Parse(diagramID)
	room, err := env.API.SyncHub().GetOrLoad(context.Background(), parsed)
	if err != nil {
		t.Fatalf("get room: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	lb, stopB := dialOwner(t, env, alice, diagramID)
	defer stopB()
	defer lb.CloseNow()
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, lb)
		_, _ = mustEnvelope(t, raw)
	}

	const writers = 4
	const perWriter = 10
	const total = writers * perWriter

	received := make(chan []byte, total*2)
	go func() {
		for {
			rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
			_, raw, err := lb.Read(rctx)
			rcan()
			if err != nil {
				return
			}
			et, p, err := sync.DecodeEnvelope(raw)
			if err != nil || et != sync.EnvelopeSync {
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
			select {
			case received <- payload:
			default:
			}
		}
	}()

	stopReaders := make(chan struct{})
	var readerWg stdsync.WaitGroup
	readerWg.Add(1)
	go func() {
		defer readerWg.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
				_ = room.StateHash()
				_ = room.Epoch()
				_ = room.PeerLen()
			}
		}
	}()

	var wg stdsync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			c, stop := dialOwner(t, env, alice, diagramID)
			defer stop()
			defer c.CloseNow()
			hctx, hcan := context.WithTimeout(context.Background(), 10*time.Second)
			defer hcan()
			var epoch int
			for i := 0; i < 2; i++ {
				raw := readRaw(t, hctx, c)
				et, p := mustEnvelope(t, raw)
				if i == 0 && et == sync.EnvelopeControl {
					ctrl, _ := sync.DecodeControl(p)
					epoch = ctrl.Epoch
				}
			}
			for j := 0; j < perWriter; j++ {
				single := crdt.New()
				key := uuid.NewString()[:8]
				single.Transact(func(txn *crdt.Transaction) {
					txn.GetMap("m").Set(txn, key, w*1000+j)
				})
				inc := crdt.EncodeStateAsUpdateV1(single, nil)
				wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
				_ = c.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, ygsync.EncodeUpdate(inc)))
				wcan()
				time.Sleep(time.Millisecond)
			}
		}(w)
	}

	for i := 0; i < 5; i++ {
		hc, hstop := dialOwner(t, env, alice, diagramID)
		hctx, hcan := context.WithTimeout(context.Background(), 5*time.Second)
		for j := 0; j < 2; j++ {
			_, _, _ = hc.Read(hctx)
		}
		hcan()
		hc.CloseNow()
		hstop()
	}

	wg.Wait()
	close(stopReaders)
	readerWg.Wait()

	deadline := time.Now().Add(15 * time.Second)
	got := 0
	for got < total && time.Now().Before(deadline) {
		select {
		case <-received:
			got++
		case <-time.After(500 * time.Millisecond):
		}
	}
	if got < total {
		t.Fatalf("listener must receive all %d broadcasts while persistence runs, got %d", total, got)
	}

	deadline = time.Now().Add(15 * time.Second)
	for {
		if n := countUpdates(t, env, diagramID); n == total {
			break
		} else if n > total {
			t.Fatalf("expected exactly %d rows, got %d", total, n)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d rows, got %d", total, countUpdates(t, env, diagramID))
		}
		time.Sleep(200 * time.Millisecond)
	}

	var nSnap int
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM sync_snapshot WHERE diagram_id = $1`, parsed).Scan(&nSnap); err != nil {
		t.Fatalf("count snapshots: %v", err)
	}
	if nSnap < 1 {
		t.Fatalf("expected >=1 snapshot under concurrent load, got %d", nSnap)
	}
}
