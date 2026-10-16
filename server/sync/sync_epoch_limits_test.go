package sync_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/reearth/ygo/crdt"
	ygsync "github.com/reearth/ygo/sync"

	"github.com/anggasct/chartdb/server/sync"
	"github.com/anggasct/chartdb/server/testhelper"
)

func putRaw(t *testing.T, client *http.Client, url, doc string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("PUT", url, bytes.NewReader([]byte(doc)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if body == nil {
		body = map[string]any{}
	}
	return resp.StatusCode, body
}

func TestEpochForceReplace(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	status, body := testhelper.Do(t, alice, "GET", env.Server.URL+"/api/v1/diagram/"+diagramID, nil)
	if status != http.StatusOK {
		t.Fatalf("get: %d %v", status, body)
	}
	epoch0, _ := body["epoch"].(float64)
	if epoch0 < 1 {
		t.Fatalf("initial epoch must be >=1, got %v", body)
	}

	ca, stopA := dialOwner(t, env, alice, diagramID)
	defer stopA()
	defer ca.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var epoch int
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, ca)
		et, p := mustEnvelope(t, raw)
		if i == 0 && et == sync.EnvelopeControl {
			ctrl, _ := sync.DecodeControl(p)
			epoch = ctrl.Epoch
		}
	}
	if epoch != int(epoch0) {
		t.Fatalf("ws epoch %d must match GET epoch %v", epoch, epoch0)
	}

	status, _ = putRaw(t, alice, env.Server.URL+"/api/v1/diagram/"+diagramID, `{"tables":[]}`)
	if status != http.StatusConflict {
		t.Fatalf("PUT without force while peers connected: %d, want 409", status)
	}

	newDoc := `{"tables":[{"name":"replaced"}],"meta":{"v":9}}`
	status, body = putRaw(t, alice, env.Server.URL+"/api/v1/diagram/"+diagramID+"?force", newDoc)
	if status != http.StatusOK {
		t.Fatalf("PUT force: %d %v", status, body)
	}
	epoch1, _ := body["epoch"].(float64)
	if int(epoch1) != int(epoch0)+1 {
		t.Fatalf("epoch must bump by 1: %v -> %v", epoch0, epoch1)
	}

	deadline := time.Now().Add(8 * time.Second)
	sawReset := false
	var resetEpoch int
	for time.Now().Before(deadline) && !sawReset {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := ca.Read(rctx)
		rcan()
		if err != nil {
			continue
		}
		et, p := mustEnvelope(t, raw)
		if et != sync.EnvelopeControl {
			continue
		}
		ctrl, err := sync.DecodeControl(p)
		if err != nil {
			continue
		}
		if ctrl.Kind == sync.ControlReset {
			sawReset = true
			resetEpoch = ctrl.Epoch
		}
	}
	if !sawReset {
		t.Fatalf("peer must receive control reset after force-replace")
	}
	if resetEpoch != int(epoch1) {
		t.Fatalf("reset epoch %d must match new epoch %v", resetEpoch, epoch1)
	}

	staleDoc := crdt.New()
	staleDoc.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("m").Set(txn, "stale", 1)
	})
	staleUpd := crdt.EncodeStateAsUpdateV1(staleDoc, nil)
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	_ = ca.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, ygsync.EncodeUpdate(staleUpd)))
	wcan()

	deadline = time.Now().Add(8 * time.Second)
	sawReset2 := false
	for time.Now().Before(deadline) && !sawReset2 {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := ca.Read(rctx)
		rcan()
		if err != nil {
			continue
		}
		et, p := mustEnvelope(t, raw)
		if et != sync.EnvelopeControl {
			continue
		}
		ctrl, _ := sync.DecodeControl(p)
		if ctrl.Kind == sync.ControlReset {
			sawReset2 = true
		}
	}
	if !sawReset2 {
		t.Fatalf("stale update must trigger control reset")
	}

	fresh := crdt.New()
	step1 := ygsync.EncodeSyncStep1(fresh)
	wctx2, wcan2 := context.WithTimeout(context.Background(), 5*time.Second)
	_ = ca.Write(wctx2, websocket.MessageBinary, sync.EncodeSyncEnvelope(int(epoch1), step1))
	wcan2()
	deadline = time.Now().Add(8 * time.Second)
	converged := false
	for time.Now().Before(deadline) && !converged {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := ca.Read(rctx)
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
		if _, err := ygsync.ApplySyncMessage(fresh, inner, nil); err == nil {
			converged = true
		}
	}
	if !converged {
		t.Fatalf("client must re-handshake and converge after reset")
	}
}

func TestLimits(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	c, stop := dialOwner(t, env, alice, diagramID)
	defer stop()
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, c)
		_, _ = mustEnvelope(t, raw)
	}
	big := make([]byte, (1<<20)+1024)
	for i := range big {
		big[i] = 'x'
	}
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	_ = c.Write(wctx, websocket.MessageBinary, big)
	wcan()
	rctx, rcan := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcan()
	_, _, err := c.Read(rctx)
	if err == nil {
		t.Fatalf("oversize message must close connection")
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("oversize close code: %d, want 1008", got)
	}

	diagram2 := createDiagram(t, env, alice)
	var conns []*websocket.Conn
	var stops []context.CancelFunc
	for i := 0; i < 50; i++ {
		cc, st := dialOwner(t, env, alice, diagram2)
		conns = append(conns, cc)
		stops = append(stops, st)
		rctx2, rcan2 := context.WithTimeout(context.Background(), 5*time.Second)
		for j := 0; j < 2; j++ {
			_, _, _ = cc.Read(rctx2)
		}
		rcan2()
	}
	defer func() {
		for _, cc := range conns {
			cc.CloseNow()
		}
		for _, st := range stops {
			st()
		}
	}()
	dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer dcancel()
	_, resp, err := websocket.Dial(dctx, wsURL(env, diagram2), &websocket.DialOptions{HTTPHeader: cookieHeader(t, env, alice)})
	if err == nil {
		t.Fatalf("51st connection must be rejected")
	}
	if resp != nil && resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("51st status: %d, want 429", resp.StatusCode)
	}
}
