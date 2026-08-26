// Package edge is the manager/edge sub-domain biz layer.
//
// Responsibilities: register new edges (issue AccessKey / SecretKey), list,
// disable, track last_seen_at, and expose AccessKeyAuthenticator as an
// AuthFunc for the tunnel server. Edge credentials are a device-level
// concern — they belong here, not in iam.
package edge
