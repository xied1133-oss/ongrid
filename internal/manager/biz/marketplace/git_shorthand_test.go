package marketplace

import "testing"

// TestExpandShorthandGitURL — accept the skills.sh `owner/repo`
// shorthand and pass real URLs / ssh-style git@host:owner/repo through
// unchanged.
func TestExpandShorthandGitURL(t *testing.T) {
	cases := []struct{ in, want string }{
		// Shorthand expansion
		{"vercel-labs/skills", "https://github.com/vercel-labs/skills.git"},
		{"owner/repo", "https://github.com/owner/repo.git"},
		{"owner/repo.git", "https://github.com/owner/repo.git"},
		{"o/r ", "https://github.com/o/r.git"},

		// Already-real URLs pass through
		{"https://github.com/vercel-labs/skills.git", "https://github.com/vercel-labs/skills.git"},
		{"https://gitlab.com/x/y", "https://gitlab.com/x/y"},
		{"git://example.com/x/y", "git://example.com/x/y"},

		// SSH-style passes through
		{"git@github.com:owner/repo.git", "git@github.com:owner/repo.git"},
		{"deploy@host.local:proj/repo", "deploy@host.local:proj/repo"},

		// Inputs that look like shorthand but are not
		{"owner.with.dots/repo", "owner.with.dots/repo"}, // dotted owner stays
		{"too/many/slashes", "too/many/slashes"},
		{"singleword", "singleword"},
		{"", ""},
	}
	for _, tc := range cases {
		got := expandShorthandGitURL(tc.in)
		if got != tc.want {
			t.Errorf("expandShorthandGitURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
