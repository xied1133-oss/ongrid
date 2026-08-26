// Package credinject resolves a skill/MCP's declarative credential
// injection (manifest requires.credentials[].inject) against a bound
// credential's decrypted fields, producing the concrete env vars + files
// to apply to the skill's exec environment (HLD-017).
//
// This is the semantics-agnostic core of credential binding: the skill
// author declares WHERE each field goes ({{field}} → ENV_VAR / file), the
// operator's binding decides WHICH credential's fields, and this package
// stitches the two. It has zero deps so it stays unit-testable and free of
// import cycles — callers pass plain maps/slices, not domain types.
package credinject

import (
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
)

// FileSpec mirrors a manifest credential file declaration (plain types).
type FileSpec struct {
	Path    string
	Content string
	Mode    string // octal string, default "0600"
}

// FilePlan is a resolved file to materialize before exec, removed after.
type FilePlan struct {
	Path    string
	Content string
	Mode    fs.FileMode
}

// Plan is the resolved injection for one credential slot.
type Plan struct {
	Env   map[string]string
	Files []FilePlan
}

var fieldRe = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_.-]+)\s*\}\}`)

// Resolve expands env + file templates against fields. Returns the Plan plus
// the sorted list of field names that were referenced but absent from fields
// (so the caller can warn "credential X is missing field Y"). A reference to
// a missing field expands to empty string in the output.
func Resolve(envSpec map[string]string, fileSpecs []FileSpec, fields map[string]string) (Plan, []string, error) {
	missing := map[string]struct{}{}
	expand := func(tmpl string) string {
		return fieldRe.ReplaceAllStringFunc(tmpl, func(m string) string {
			key := strings.TrimSpace(fieldRe.FindStringSubmatch(m)[1])
			if v, ok := fields[key]; ok {
				return v
			}
			missing[key] = struct{}{}
			return ""
		})
	}

	plan := Plan{Env: map[string]string{}}
	for name, tmpl := range envSpec {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		plan.Env[name] = expand(tmpl)
	}
	for _, f := range fileSpecs {
		path := strings.TrimSpace(f.Path)
		if path == "" {
			continue
		}
		mode := fs.FileMode(0o600)
		if strings.TrimSpace(f.Mode) != "" {
			parsed, err := strconv.ParseUint(strings.TrimSpace(f.Mode), 8, 32)
			if err != nil {
				return Plan{}, nil, fmt.Errorf("credinject: bad file mode %q: %w", f.Mode, err)
			}
			mode = fs.FileMode(parsed)
		}
		plan.Files = append(plan.Files, FilePlan{Path: path, Content: expand(f.Content), Mode: mode})
	}

	out := make([]string, 0, len(missing))
	for k := range missing {
		out = append(out, k)
	}
	sortStrings(out)
	return plan, out, nil
}

// sortStrings is a tiny insertion sort — avoids importing sort for one call.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
