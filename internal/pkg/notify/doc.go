// Package notify provides transport adapters for outbound notifications.
//
// It is intentionally business-agnostic: alerting, scheduled tasks, and
// AIOps proactive jobs should pass a normalized Message into Sender instead
// of importing Slack, Feishu, DingTalk, or generic webhook details.
package notify
