package sync_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reearth/ygo/crdt"
)

func TestWireCompatYjsFixture(t *testing.T) {
	base := "testdata/yjs13-update.bin"
	raw, err := os.ReadFile(filepath.Clean(base))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ver, err := os.ReadFile("testdata/yjs13-update.version")
	if err != nil {
		t.Fatalf("read version: %v", err)
	}
	if !strings.Contains(string(ver), "13.") {
		t.Fatalf("fixture must carry yjs@13 generator version, got %q", ver)
	}
	expRaw, err := os.ReadFile("testdata/yjs13-update.expected.json")
	if err != nil {
		t.Fatalf("read expected: %v", err)
	}
	var expected any
	if err := json.Unmarshal(expRaw, &expected); err != nil {
		t.Fatalf("parse expected: %v", err)
	}
	expCanon, _ := json.Marshal(expected)

	doc := crdt.New()
	if err := crdt.ApplyUpdateV1(doc, raw, nil); err != nil {
		t.Fatalf("apply yjs update in ygo: %v", err)
	}
	got, ok := JSONFromDocForTest(doc)
	if !ok {
		t.Fatalf("missing diagram content after apply")
	}
	var gotAny any
	if err := json.Unmarshal([]byte(got), &gotAny); err != nil {
		t.Fatalf("parse applied content: %v", err)
	}
	gotCanon, _ := json.Marshal(gotAny)
	if string(gotCanon) != string(expCanon) {
		t.Fatalf("content mismatch: got %s want %s", gotCanon, expCanon)
	}
}

func JSONFromDocForTest(doc *crdt.Doc) (string, bool) {
	m := doc.GetMap("diagram")
	if m == nil {
		return "", false
	}
	v, ok := m.Get("content")
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}
