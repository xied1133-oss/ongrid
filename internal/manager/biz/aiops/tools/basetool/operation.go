package basetool

// Operation is a durable, user-visible outcome returned by an asynchronous
// tool. Tool-specific data remains alongside it; clients use this stable
// envelope to render progress, navigation, and eventual actions consistently.
type Operation struct {
	Kind    string            `json:"kind"`
	ID      string            `json:"id"`
	State   string            `json:"state"`
	Title   string            `json:"title"`
	Summary string            `json:"summary,omitempty"`
	Links   map[string]string `json:"links,omitempty"`
	Actions []OperationAction `json:"actions,omitempty"`
}

type OperationAction struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Tool    string `json:"tool,omitempty"`
	Enabled bool   `json:"enabled"`
}
