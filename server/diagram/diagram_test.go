package diagram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/anggasct/chartdb/server/testhelper"
)

func createDiagram(t *testing.T, env *testhelper.Env, client *http.Client, title, doc string) string {
	t.Helper()
	status, body := testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/diagram", map[string]any{"title": title, "document": json.RawMessage(doc)})
	if status != http.StatusCreated {
		t.Fatalf("create diagram status %d body %v", status, body)
	}
	version, _ := body["version"].(float64)
	if version != 1 {
		t.Fatalf("new diagram must start at version 1, got %v", body)
	}
	id, _ := body["id"].(string)
	if id == "" {
		t.Fatalf("create diagram missing id: %v", body)
	}
	return id
}

func putRaw(t *testing.T, env *testhelper.Env, client *http.Client, id, doc string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest("PUT", env.Server.URL+"/api/v1/diagram/"+id, bytes.NewReader([]byte(doc)))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("put diagram: %v", err)
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

func versionCount(t *testing.T, env *testhelper.Env, diagramID string) int {
	t.Helper()
	var n int
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM diagram_version WHERE diagram_id = $1`, diagramID).Scan(&n); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	return n
}

func assertInvariant(t *testing.T, env *testhelper.Env, diagramID string) {
	t.Helper()
	var head, max int
	var doc, headDoc string
	err := env.Store.DB.QueryRowContext(context.Background(), `SELECT head_version, document::text FROM diagram WHERE id = $1`, diagramID).Scan(&head, &doc)
	if err != nil {
		t.Fatalf("read diagram: %v", err)
	}
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT COALESCE(MAX(version), 0) FROM diagram_version WHERE diagram_id = $1`, diagramID).Scan(&max); err != nil {
		t.Fatalf("max version: %v", err)
	}
	if head != max {
		t.Fatalf("invariant violated: head_version %d != MAX(version) %d", head, max)
	}
	if err := env.Store.DB.QueryRowContext(context.Background(), `SELECT document::text FROM diagram_version WHERE diagram_id = $1 AND version = $2`, diagramID, head).Scan(&headDoc); err != nil {
		t.Fatalf("head version doc: %v", err)
	}
	if doc != headDoc {
		t.Fatalf("invariant violated: diagram.document differs from HEAD version document")
	}
}

func TestDiagramVersions(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("editor"), "password-123")

	v1 := `{"tables":[{"name":"users"}],"meta":{"v":1}}`
	id := createDiagram(t, env, client, "Shop", v1)

	v2 := `{"tables":[{"name":"users"},{"name":"orders"}],"meta":{"v":2}}`
	status, body := putRaw(t, env, client, id, v2)
	if status != http.StatusOK {
		t.Fatalf("put status %d body %v", status, body)
	}
	if body["version"] != float64(2) {
		t.Fatalf("second save must be version 2, got %v", body)
	}
	if n := versionCount(t, env, id); n != 2 {
		t.Fatalf("expected 2 version rows, got %d", n)
	}
	assertInvariant(t, env, id)

	status, body = putRaw(t, env, client, id, v2)
	if status != http.StatusOK {
		t.Fatalf("identical put status %d", status)
	}
	if body["version"] != float64(2) {
		t.Fatalf("identical bytes must not bump the version, got %v", body)
	}
	if n := versionCount(t, env, id); n != 2 {
		t.Fatalf("identical put must not append history, rows %d", n)
	}

	status, body = testhelper.Do(t, client, "POST", env.Server.URL+"/api/v1/diagram/"+id+"/restore", map[string]int{"version": 1})
	if status != http.StatusOK {
		t.Fatalf("restore status %d body %v", status, body)
	}
	if body["version"] != float64(3) {
		t.Fatalf("restore must append version 3, got %v", body)
	}
	status, body = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/"+id, nil)
	if status != http.StatusOK {
		t.Fatalf("get after restore status %d", status)
	}
	got, _ := json.Marshal(body["document"])
	var wantAny any
	if err := json.Unmarshal([]byte(v1), &wantAny); err != nil {
		t.Fatalf("parse v1: %v", err)
	}
	want, _ := json.Marshal(wantAny)
	if string(got) != string(want) {
		t.Fatalf("HEAD after restore must equal version 1 bytes: %s vs %s", got, want)
	}
	if n := versionCount(t, env, id); n != 3 {
		t.Fatalf("restore must append history, rows %d", n)
	}
	assertInvariant(t, env, id)

	v3 := `{"tables":[{"name":"users"}],"meta":{"v":3}}`
	status, body = putRaw(t, env, client, id, v3)
	if status != http.StatusOK {
		t.Fatalf("post-restore put status %d body %v", status, body)
	}
	if body["version"] != float64(4) {
		t.Fatalf("post-restore save must be version 4, got %v", body)
	}
	assertInvariant(t, env, id)

	status, body = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/"+id+"/version", nil)
	if status != http.StatusOK {
		t.Fatalf("list versions status %d", status)
	}
	versions, _ := body["versions"].([]any)
	if len(versions) != 4 {
		t.Fatalf("expected 4 versions newest-first, got %v", body)
	}
	first, _ := versions[0].(map[string]any)
	if first["version"] != float64(4) {
		t.Fatalf("versions must be newest-first: %v", body)
	}

	status, body = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/"+id+"/version/1", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 status %d", status)
	}
	got, _ = json.Marshal(body["document"])
	if string(got) != string(want) {
		t.Fatalf("version 1 document mismatch: %s", got)
	}
}

func TestDiagramListAndDelete(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("lister"), "password-123")

	for _, title := range []string{"One", "Two", "Three"} {
		createDiagram(t, env, client, title, `{"n":1}`)
	}
	status, body := testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram?limit=2", nil)
	if status != http.StatusOK {
		t.Fatalf("list status %d", status)
	}
	items, _ := body["diagrams"].([]any)
	next, _ := body["next_cursor"].(string)
	if len(items) != 2 || next == "" {
		t.Fatalf("expected first page of 2 plus cursor, got %v", body)
	}
	status, body = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram?limit=2&cursor="+next, nil)
	if status != http.StatusOK {
		t.Fatalf("second page status %d", status)
	}
	items, _ = body["diagrams"].([]any)
	if len(items) != 1 {
		t.Fatalf("expected remaining 1 diagram, got %v", body)
	}

	id := createDiagram(t, env, client, "Gone", `{"n":1}`)
	status, _ = testhelper.Do(t, client, "DELETE", env.Server.URL+"/api/v1/diagram/"+id, nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status %d, want 204", status)
	}
	status, _ = testhelper.Do(t, client, "GET", env.Server.URL+"/api/v1/diagram/"+id, nil)
	if status != http.StatusNotFound {
		t.Fatalf("get after delete status %d, want 404", status)
	}
}

func TestConcurrentSavesAllocateDistinctVersions(t *testing.T) {
	env := testhelper.Boot(t, nil)
	client := env.Client()
	env.Register(t, client, testhelper.UniqueEmail("concurrent"), "password-123")

	id := createDiagram(t, env, client, "Racy", `{"n":0}`)
	docs := []string{`{"n":1}`, `{"n":2}`}
	type result struct {
		status  int
		version float64
	}
	out := make([]result, len(docs))
	var wg sync.WaitGroup
	for i, doc := range docs {
		wg.Add(1)
		go func(i int, doc string) {
			defer wg.Done()
			status, body := putRaw(t, env, client, id, doc)
			v, _ := body["version"].(float64)
			out[i] = result{status: status, version: v}
		}(i, doc)
	}
	wg.Wait()
	for i, r := range out {
		if r.status != http.StatusOK {
			t.Fatalf("concurrent save %d status %d, want 200", i, r.status)
		}
	}
	if out[0].version == out[1].version {
		t.Fatalf("concurrent saves must yield distinct versions, got %v", out)
	}
	lo, hi := out[0].version, out[1].version
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo != 2 || hi != 3 {
		t.Fatalf("concurrent saves must yield versions 2 and 3, got %v", out)
	}
	if n := versionCount(t, env, id); n != 3 {
		t.Fatalf("expected 3 version rows after concurrent saves, got %d", n)
	}
	assertInvariant(t, env, id)
}
