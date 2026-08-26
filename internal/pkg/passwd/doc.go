// Package passwd provides the shared argon2id Hash / Verify helpers used
// for any "secret plaintext -> PHC-encoded hash" need across BCs.
//
// Originally lived in internal/iam/biz/user/hash.go. It was promoted to a
// shared utility because manager/biz/edge needs the same scheme to hash
// SecretKey material, and arch-lint forbids manager -> iam imports. Both
// iam (user passwords) and manager (edge secret keys) now import this
// package; the iam wrapper keeps the old unexported names to avoid a wide
// refactor of existing iam tests.
//
// Format: $argon2id$v=19$m=65536,t=1,p=4$<salt-b64>$<hash-b64>
package passwd
