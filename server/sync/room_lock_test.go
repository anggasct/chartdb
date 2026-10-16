package sync

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/reearth/ygo/crdt"
)

func TestRoomConcurrentReadReplaceNoRace(t *testing.T) {
	h := NewHub(nil, 1<<20, 50, 500, time.Second)
	r := newRoom(uuid.New(), crdt.New(), 1, 0, h)
	r.startExpiryLoop()
	defer r.destroy()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = r.StateHash()
				_, _ = r.handshakeState()
				_ = r.Epoch()
				_ = r.PeerLen()
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				single := crdt.New()
				key := string(rune('a' + n))
				single.Transact(func(txn *crdt.Transaction) {
					txn.GetMap("m").Set(txn, key, j)
				})
				state := crdt.EncodeStateAsUpdateV1(single, nil)
				r.ResetForForce(state, 1+j, 0)
			}
		}(i)
	}
	wg.Wait()
}
