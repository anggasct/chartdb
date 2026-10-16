package sync

import (
	"github.com/reearth/ygo/crdt"
)

func BaseUpdateFromJSON(raw []byte) []byte {
	doc := crdt.New()
	s := string(raw)
	doc.Transact(func(txn *crdt.Transaction) {
		txn.GetMap("diagram").Set(txn, "content", s)
	})
	return crdt.EncodeStateAsUpdateV1(doc, nil)
}

func JSONFromDoc(doc *crdt.Doc) (string, bool) {
	m := doc.GetMap("diagram")
	if m == nil {
		return "", false
	}
	v, ok := m.Get("content")
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return s, true
}
