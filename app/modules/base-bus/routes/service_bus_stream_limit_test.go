package routes

import "testing"

func TestServiceBusStreamSlotsPerUserAndPerDB(t *testing.T) {
	s := &serviceBusStreamSlots{perDB: map[string]int{}, perKey: map[string]int{}}

	// Per user: the cap holds even when the DB has room.
	releases := make([]func(), 0, serviceBusMaxStreamsPerUser)
	for i := 0; i < serviceBusMaxStreamsPerUser; i++ {
		release, ok := s.acquire("t1", 7, 100)
		if !ok {
			t.Fatalf("stream %d should fit the per-user cap", i+1)
		}
		releases = append(releases, release)
	}
	if s.available("t1", 7, 100) {
		t.Fatalf("available must refuse past the per-user cap")
	}
	if _, ok := s.acquire("t1", 7, 100); ok {
		t.Fatalf("acquire must refuse past the per-user cap")
	}
	if _, ok := s.acquire("t2", 7, 100); !ok {
		t.Fatalf("the same uid on another DB is another user")
	}

	// Release is idempotent and frees exactly one slot.
	releases[0]()
	releases[0]()
	if _, ok := s.acquire("t1", 7, 100); !ok {
		t.Fatalf("a released slot must be reusable")
	}
	if _, ok := s.acquire("t1", 7, 100); ok {
		t.Fatalf("a double release must not free two slots")
	}

	// Per DB: other users are refused once the DB share is used.
	if _, ok := s.acquire("t1", 8, serviceBusMaxStreamsPerUser); ok {
		t.Fatalf("acquire must refuse past the per-DB limit")
	}
}
