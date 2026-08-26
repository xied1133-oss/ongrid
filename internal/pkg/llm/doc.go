// Package llm is a thin OpenAI-style chat/tool-calling client with a
// per-day token budget hook and Prom instrumentation.
//
// Red line: no provider abstraction — interface shape follows
// OpenAI's. SDK is github.com/sashabaranov/go-openai. No auto-retry (tools
// are not idempotent); no streaming.
//
// Red line: Prom labels MUST NOT contain user_id / org_id /
// session_id. The only labels used here are model, kind, result.
//
// Red line: NEVER log user message content. Log token counts, tool call
// names and counts, durations, and user_id (safe — not PII).
package llm
