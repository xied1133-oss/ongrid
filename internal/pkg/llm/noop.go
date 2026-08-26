package llm

import "context"

// noopClient is returned by New when cfg.APIKey is empty. Chat always fails
// with ErrNoAPIKey; the main binary uses this during local dev so the agent
// wiring still compiles and runs until an operator provides a real key.
type noopClient struct{}

func (noopClient) Chat(ctx context.Context, req ChatReq) (*ChatResp, error) {
	_ = ctx
	_ = req
	return nil, ErrNoAPIKey
}
