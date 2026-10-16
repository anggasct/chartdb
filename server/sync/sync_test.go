package sync_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/reearth/ygo/awareness"
	"github.com/reearth/ygo/crdt"
	ygsync "github.com/reearth/ygo/sync"

	"github.com/anggasct/chartdb/server/sync"
	"github.com/anggasct/chartdb/server/testhelper"
)

func wsURL(env *testhelper.Env, diagramID string) string {
	u := strings.Replace(env.Server.URL, "http://", "ws://", 1)
	u = strings.Replace(u, "https://", "wss://", 1)
	return u + "/api/v1/diagram/" + diagramID + "/sync"
}

func cookieHeader(t *testing.T, env *testhelper.Env, client *http.Client) http.Header {
	t.Helper()
	u, _ := url.Parse(env.Server.URL)
	var sb strings.Builder
	for _, c := range client.Jar.Cookies(u) {
		if sb.Len() > 0 {
			sb.WriteString("; ")
		}
		sb.WriteString(c.Name + "=" + c.Value)
	}
	return http.Header{"Cookie": []string{sb.String()}}
}

func dialOwner(t *testing.T, env *testhelper.Env, client *http.Client, diagramID string) (*websocket.Conn, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	c, _, err := websocket.Dial(ctx, wsURL(env, diagramID), &websocket.DialOptions{HTTPHeader: cookieHeader(t, env, client)})
	if err != nil {
		cancel()
		t.Fatalf("dial: %v", err)
	}
	return c, cancel
}

func readRaw(t *testing.T, ctx context.Context, c *websocket.Conn) []byte {
	t.Helper()
	_, raw, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return raw
}

func mustEnvelope(t *testing.T, raw []byte) (int, []byte) {
	t.Helper()
	env, payload, err := sync.DecodeEnvelope(raw)
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env, payload
}

func createDiagram(t *testing.T, env *testhelper.Env, client *http.Client) string {
	t.Helper()
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/diagram", map[string]any{"title": "Sync", "document": map[string]any{"tables": []any{}}})
	if status != http.StatusCreated {
		t.Fatalf("create diagram: %d %v", status, body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %v", body)
	}
	return id
}

func TestAdmission(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, wsURL(env, diagramID), nil)
	if err == nil {
		t.Fatalf("anon dial must fail")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anon status: %d, want 401", resp.StatusCode)
	}

	bob := env.Client()
	env.Register(t, bob, testhelper.UniqueEmail("bob"), "password-123")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_, resp, err = websocket.Dial(ctx2, wsURL(env, diagramID), &websocket.DialOptions{HTTPHeader: cookieHeader(t, env, bob)})
	if err == nil {
		t.Fatalf("foreign dial must fail")
	}
	if resp != nil && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign status: %d, want 403", resp.StatusCode)
	}

	c, stop := dialOwner(t, env, alice, diagramID)
	defer stop()
	defer c.CloseNow()
	rctx, rcan := context.WithTimeout(context.Background(), 5*time.Second)
	defer rcan()

	raw := readRaw(t, rctx, c)
	envType, payload := mustEnvelope(t, raw)
	if envType != sync.EnvelopeControl {
		t.Fatalf("first message must be control, got %d", envType)
	}
	ctrl, err := sync.DecodeControl(payload)
	if err != nil || ctrl.Kind != sync.ControlEpoch || ctrl.Epoch < 1 {
		t.Fatalf("control epoch: %v %v", ctrl, err)
	}
	epoch := ctrl.Epoch

	raw = readRaw(t, rctx, c)
	envType, payload = mustEnvelope(t, raw)
	if envType != sync.EnvelopeSync {
		t.Fatalf("second message must be sync, got %d", envType)
	}
	msgEpoch, inner, err := sync.DecodeSyncPayload(payload)
	if err != nil || msgEpoch != epoch {
		t.Fatalf("server step1 epoch: %d %v", msgEpoch, err)
	}
	msgType, _, err := ygsync.ReadSyncMessage(inner)
	if err != nil || msgType != ygsync.MsgSyncStep1 {
		t.Fatalf("server step1 type: %d %v", msgType, err)
	}

	clientDoc := crdt.New()
	reply, err := ygsync.ApplySyncMessage(clientDoc, inner, nil)
	if err != nil {
		t.Fatalf("apply server step1: %v", err)
	}
	if len(reply) > 0 {
		wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
		if err := c.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, reply)); err != nil {
			wcan()
			t.Fatalf("write client step2: %v", err)
		}
		wcan()
	}

	step1 := ygsync.EncodeSyncStep1(clientDoc)
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	if err := c.Write(wctx, websocket.MessageBinary, sync.EncodeSyncEnvelope(epoch, step1)); err != nil {
		wcan()
		t.Fatalf("write client step1: %v", err)
	}
	wcan()

	raw = readRaw(t, rctx, c)
	envType, payload = mustEnvelope(t, raw)
	if envType != sync.EnvelopeSync {
		t.Fatalf("expected sync step2, got %d", envType)
	}
	msgEpoch, inner, err = sync.DecodeSyncPayload(payload)
	if err != nil || msgEpoch != epoch {
		t.Fatalf("server step2 epoch: %v", err)
	}
	msgType, _, err = ygsync.ReadSyncMessage(inner)
	if err != nil || msgType != ygsync.MsgSyncStep2 {
		t.Fatalf("server step2 type: %d %v", msgType, err)
	}
	if _, err := ygsync.ApplySyncMessage(clientDoc, inner, nil); err != nil {
		t.Fatalf("apply server step2: %v", err)
	}
}

func TestGuestSocketRefused(t *testing.T) {
	env := testhelper.Boot(t, nil)
	owner := env.Client()
	env.Register(t, owner, testhelper.UniqueEmail("owner"), "password-123")
	diagramID := createDiagram(t, env, owner)

	status, body := testhelper.Do(t, owner, "POST", env.Server.URL+"/api/v1/diagram/"+diagramID+"/share", map[string]any{})
	if status != http.StatusCreated {
		t.Fatalf("share create: %d %v", status, body)
	}
	token, _ := body["token"].(string)
	if token == "" {
		t.Fatalf("missing token: %v", body)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shareURL := wsURL(env, diagramID) + "?token=" + url.QueryEscape(token)
	_, resp, err := websocket.Dial(ctx, shareURL, nil)
	if err == nil {
		t.Fatalf("guest ws must fail")
	}
	if resp != nil && resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("guest ws status: %d, want 401/403", resp.StatusCode)
	}

	anon := &http.Client{}
	req, _ := http.NewRequest("GET", env.Server.URL+"/api/v1/s/"+token, nil)
	r, err := anon.Do(req)
	if err != nil {
		t.Fatalf("share read: %v", err)
	}
	defer r.Body.Close()
	if r.StatusCode != http.StatusOK {
		t.Fatalf("share read status: %d, want 200", r.StatusCode)
	}
}

func stateHash(doc *crdt.Doc) string {
	sum := sha256.Sum256(crdt.EncodeStateAsUpdateV1(doc, nil))
	return string(sum[:])
}

func TestConvergence(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("a"), "password-123")
	diagramID := createDiagram(t, env, alice)
	parsed, _ := uuid.Parse(diagramID)
	room, err := env.API.SyncHub().GetOrLoad(context.Background(), parsed)
	if err != nil {
		t.Fatalf("get room: %v", err)
	}

	a := crdt.New()
	b := crdt.New()
	a.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("tables").Set(txn, "t1", "users")
	})
	b.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("tables").Set(txn, "t2", "orders")
	})
	aHash0 := stateHash(a)
	bHash0 := stateHash(b)
	if aHash0 == bHash0 {
		t.Fatalf("precondition: concurrent docs must diverge")
	}

	updA := crdt.EncodeStateAsUpdateV1(a, nil)
	updB := crdt.EncodeStateAsUpdateV1(b, nil)
	if err := crdt.ApplyUpdateV1(room.Doc(), updA, "a"); err != nil {
		t.Fatalf("apply A to hub: %v", err)
	}
	if err := crdt.ApplyUpdateV1(room.Doc(), updB, "b"); err != nil {
		t.Fatalf("apply B to hub: %v", err)
	}
	hubUpdate := crdt.EncodeStateAsUpdateV1(room.Doc(), nil)
	if err := crdt.ApplyUpdateV1(a, hubUpdate, nil); err != nil {
		t.Fatalf("apply hub to A: %v", err)
	}
	if err := crdt.ApplyUpdateV1(b, hubUpdate, nil); err != nil {
		t.Fatalf("apply hub to B: %v", err)
	}
	if stateHash(a) != stateHash(b) || stateHash(a) != stateHash(room.Doc()) {
		t.Fatalf("docs must converge to identical hash")
	}
	v, ok := mustGet(a, "t1")
	if !ok || v != "users" {
		t.Fatalf("A must retain t1=users, got %v", v)
	}
	v, ok = mustGet(a, "t2")
	if !ok || v != "orders" {
		t.Fatalf("A must retain t2=orders, got %v", v)
	}
	_ = env
}

func mustGet(doc *crdt.Doc, key string) (any, bool) {
	m := doc.GetMap("tables")
	if m == nil {
		return nil, false
	}
	return m.Get(key)
}

func TestPresence(t *testing.T) {
	env := testhelper.Boot(t, nil)
	alice := env.Client()
	env.Register(t, alice, testhelper.UniqueEmail("alice"), "password-123")
	diagramID := createDiagram(t, env, alice)

	ca, stopA := dialOwner(t, env, alice, diagramID)
	defer stopA()
	defer ca.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctx, ca)
		_, _ = mustEnvelope(t, raw)
	}

	awareA := awareness.New(42)
	awareA.SetLocalState(map[string]any{"user": map[string]any{"id": "a", "name": "Alice", "color": "red"}, "selected": []any{}})
	payloadA := awareA.EncodeUpdate(nil)
	wctx, wcan := context.WithTimeout(context.Background(), 5*time.Second)
	_ = ca.Write(wctx, websocket.MessageBinary, sync.EncodeAwarenessEnvelope(payloadA))
	wcan()

	bob := env.Client()
	env.Register(t, bob, testhelper.UniqueEmail("bob"), "password-123")
	status, _ := testhelper.Do(t, bob, "GET", env.Server.URL+"/api/v1/diagram/"+diagramID, nil)
	if status != http.StatusForbidden {
		t.Fatalf("bob must not own diagram (precondition): %d", status)
	}

	cb, stopB := dialOwner(t, env, alice, diagramID)
	defer stopB()
	ctxB, cancelB := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelB()
	for i := 0; i < 2; i++ {
		raw := readRaw(t, ctxB, cb)
		_, _ = mustEnvelope(t, raw)
	}
	awareB := awareness.New(77)
	awareB.SetLocalState(map[string]any{"user": map[string]any{"id": "b", "name": "Bob", "color": "blue"}, "selected": []any{"t1"}})
	payloadB := awareB.EncodeUpdate(nil)
	wctx2, wcan2 := context.WithTimeout(context.Background(), 5*time.Second)
	_ = cb.Write(wctx2, websocket.MessageBinary, sync.EncodeAwarenessEnvelope(payloadB))
	wcan2()

	deadline := time.Now().Add(8 * time.Second)
	sawBob := false
	for time.Now().Before(deadline) && !sawBob {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := ca.Read(rctx)
		rcan()
		if err != nil {
			continue
		}
		et, p := mustEnvelope(t, raw)
		if et != sync.EnvelopeAwareness {
			continue
		}
		tmp := awareness.New(999)
		if err := tmp.ApplyUpdate(p, nil); err != nil {
			continue
		}
		for _, st := range tmp.GetStates() {
			bs, _ := json.Marshal(st.State)
			if strings.Contains(string(bs), "Bob") && strings.Contains(string(bs), "blue") {
				sawBob = true
			}
		}
	}
	if !sawBob {
		t.Fatalf("A must observe B presence with name/color")
	}

	_ = cb.Close(websocket.StatusNormalClosure, "")
	seen := awareness.New(999)
	_ = seen.ApplyUpdate(payloadB, nil)
	deadline = time.Now().Add(30 * time.Second)
	sawRemoval := false
	for time.Now().Before(deadline) && !sawRemoval {
		rctx, rcan := context.WithTimeout(context.Background(), 2*time.Second)
		_, raw, err := ca.Read(rctx)
		rcan()
		if err != nil {
			if websocket.CloseStatus(err) != -1 {
				break
			}
			continue
		}
		et, p := mustEnvelope(t, raw)
		if et != sync.EnvelopeAwareness {
			continue
		}
		_ = seen.ApplyUpdate(p, nil)
		if _, ok := seen.GetStates()[77]; !ok {
			sawRemoval = true
		}
	}
	if !sawRemoval {
		t.Fatalf("A must observe B removal within 30s")
	}
}
