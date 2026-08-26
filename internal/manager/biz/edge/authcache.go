package edge

import (
	"crypto/sha256"
	"sync"
	"time"
)

// authCacheTTL is how long a successful credential verification is cached.
// 60 seconds covers typical high-frequency push bursts while ensuring
// revoked keys stop working within a bounded window.
const authCacheTTL = 60 * time.Second

// authCache caches successful argon2id verifications to avoid the 64 MiB
// per-call cost on every data-plane auth_request. Keys are the SHA-256 of
// accessKey + ":" + secretKey so plaintext credentials are never stored.
type authCache struct {
	mu      sync.Mutex
	entries map[[32]byte]authCacheEntry
}

type authCacheEntry struct {
	edgeID    uint64
	expiresAt time.Time
}

func newAuthCache() *authCache {
	return &authCache{entries: make(map[[32]byte]authCacheEntry)}
}

func (c *authCache) key(accessKey, secretKey string) [32]byte {
	return sha256.Sum256([]byte(accessKey + ":" + secretKey))
}

// lookup returns (edgeID, true) if the credential pair has a valid cache hit.
func (c *authCache) lookup(accessKey, secretKey string) (uint64, bool) {
	k := c.key(accessKey, secretKey)
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	if !ok || time.Now().After(e.expiresAt) {
		if ok {
			delete(c.entries, k)
		}
		return 0, false
	}
	return e.edgeID, true
}

// store records a successful verification result.
func (c *authCache) store(accessKey, secretKey string, edgeID uint64) {
	k := c.key(accessKey, secretKey)
	c.mu.Lock()
	c.entries[k] = authCacheEntry{edgeID: edgeID, expiresAt: time.Now().Add(authCacheTTL)}
	c.mu.Unlock()
}
