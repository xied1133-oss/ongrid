package imbridge

import "sync"

// dedupSet is a bounded, concurrency-safe set of recently-seen event keys.
// It exists because long-poll providers (Telegram getUpdates) re-deliver
// updates that weren't acked yet: the poll offset lives in the StreamClient,
// which the supervisor recreates from offset 0 on every reconnect, so a tunnel
// hiccup right after a batch arrives makes Telegram hand the same updates back
// — and we'd run the agent + post a reply twice. The bridge (a process-lifetime
// singleton) dedups by event id across those reconnects.
//
// Bounding uses a two-generation map: when cur fills, it becomes prev and a
// fresh cur starts. That retains between cap and 2*cap of the most recent keys
// with O(1) inserts and no per-entry timestamps. It is in-memory only — a full
// manager restart resets it (and the Telegram offset), so a restart can still
// reprocess the unacked backlog; that's a far rarer event than a reconnect.
type dedupSet struct {
	mu   sync.Mutex
	cap  int
	cur  map[string]struct{}
	prev map[string]struct{}
}

func newDedupSet(capacity int) *dedupSet {
	if capacity < 1 {
		capacity = 1
	}
	return &dedupSet{
		cap:  capacity,
		cur:  make(map[string]struct{}, capacity),
		prev: make(map[string]struct{}),
	}
}

// seenOrAdd returns true if key was already recorded; otherwise it records the
// key and returns false. The caller treats a true result as "duplicate, skip".
func (d *dedupSet) seenOrAdd(key string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.cur[key]; ok {
		return true
	}
	if _, ok := d.prev[key]; ok {
		return true
	}
	if len(d.cur) >= d.cap {
		d.prev = d.cur
		d.cur = make(map[string]struct{}, d.cap)
	}
	d.cur[key] = struct{}{}
	return false
}
