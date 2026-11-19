package routes

// File overview:
// - Responsibility: caps concurrent /bus/ws and /bus/sse push streams (plan T30).
// - Key symbols: `serviceBusStreamSlots`, `streamSlots`.
// - Why: streams share one LISTEN connection per DB (plan T70, service_bus_listener.go) and only
//   borrow a pool connection per poll, so the per-DB cap is a goroutine/memory guard, not a pool
//   share. Per user, at most serviceBusMaxStreamsPerUser (the SPA elects one leader tab per
//   browser, so a handful covers several browsers/devices). A refused client gets 429 on the
//   handshake, which the bus client counts as a failure and eventually falls back to polling.

import (
	"strconv"
	"sync"
)

const (
	serviceBusMaxStreamsPerUser = 8
	serviceBusMaxStreamsPerDB   = 1000
)

type serviceBusStreamSlots struct {
	mu     sync.Mutex
	perDB  map[string]int
	perKey map[string]int
}

var streamSlots = &serviceBusStreamSlots{perDB: map[string]int{}, perKey: map[string]int{}}

func serviceBusStreamUserKey(dbName string, userID int64) string {
	return dbName + "|" + strconv.FormatInt(userID, 10)
}

// available is the pre-check run before the WS upgrade / SSE response so a
// refused client gets a real 429. acquire re-checks under the same lock and
// is the hard bound.
func (s *serviceBusStreamSlots) available(dbName string, userID int64, dbLimit int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.perDB[dbName] < dbLimit && s.perKey[serviceBusStreamUserKey(dbName, userID)] < serviceBusMaxStreamsPerUser
}

// acquire takes one slot; release must be called exactly once when ok.
func (s *serviceBusStreamSlots) acquire(dbName string, userID int64, dbLimit int) (release func(), ok bool) {
	key := serviceBusStreamUserKey(dbName, userID)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perDB[dbName] >= dbLimit || s.perKey[key] >= serviceBusMaxStreamsPerUser {
		return nil, false
	}
	s.perDB[dbName]++
	s.perKey[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.perDB[dbName]--; s.perDB[dbName] <= 0 {
				delete(s.perDB, dbName)
			}
			if s.perKey[key]--; s.perKey[key] <= 0 {
				delete(s.perKey, key)
			}
		})
	}, true
}
