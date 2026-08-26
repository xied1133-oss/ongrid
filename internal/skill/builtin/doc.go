// Package builtin holds the canonical L2 skill implementations. Each
// concrete skill registers itself via skill.Register in init(), so
// importing this package is the only thing needed to make all bundled
// skills available to the dispatcher (edge) and the manager (metadata
// + AI tools).
//
// New skills land here as one file each — the framework auto-derives
// the LLM tool registration, the HTTP API, the UI form, the
// permission gate, and the audit log from skill metadata.
//
// Sub-packaged skills (one per directory) are blank-imported below so
// the same `_ "internal/skill/builtin"` import in cmd/ongrid + cmd/
// ongrid-edge transitively wires every skill regardless of layout.
// New mutating / dangerous skills live in their own
// sub-packages so the registration shim and any auxiliary types stay
// out of the flat builtin namespace.
package builtin

// Blank imports — each side-effect-only init() call on these
// sub-packages registers a skill with the global registry. Adding a
// new mutating-class skill: write the file under its own subdir and
// add a one-line blank import here.
import (
	_ "github.com/ongridio/ongrid/internal/skill/builtin/restart_service"
)
