// Package user is the iam BC's only sub-domain: user accounts and their
// authentication flows.
//
// Responsibilities (private MVP):
//   - Register: create user with argon2id-hashed password. Role enforcement
//     (admin-only registration) is the caller's job — biz is policy-neutral.
//   - Login: verify credentials, issue an access + refresh JWT pair.
//   - Refresh: rotate a pair from a valid refresh token (signature-trust,
//     no server-side revocation list in MVP).
//   - BootstrapAdmin: idempotent seed of the very first admin on a fresh DB.
//   - Admin-side list / delete / update-role.
//
// The package never returns a user's PassHash across its public API.
package user
