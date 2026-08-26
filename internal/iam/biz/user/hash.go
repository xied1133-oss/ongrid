package user

import "github.com/ongridio/ongrid/internal/pkg/passwd"

// hashPassword is a thin wrapper around passwd.Hash. The argon2id helpers
// were promoted to internal/pkg/passwd so manager/biz/edge can reuse the
// same scheme for its SecretKeyHash without crossing the iam BC boundary
// (arch-lint forbids manager -> iam imports).
func hashPassword(password string) (string, error) {
	return passwd.Hash(password)
}

// verifyPassword is a thin wrapper around passwd.Verify; see hashPassword.
func verifyPassword(password, encoded string) bool {
	return passwd.Verify(password, encoded)
}
