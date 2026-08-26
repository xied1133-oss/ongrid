package knowledge

import "testing"

func TestExtractDocTitle(t *testing.T) {
	cases := []struct {
		name string
		body string
		path string
		want string
	}{
		{
			name: "yaml frontmatter title wins",
			body: "---\ntitle: 真实的标题\nauthor: x\n---\n\n# 不该用这个 H1\n\nbody",
			path: "reference/external/x/abc123-slug.md",
			want: "真实的标题",
		},
		{
			name: "h1 used when no frontmatter",
			body: "# 真正的 H1 标题\n\nrest...",
			path: "x.md",
			want: "真正的 H1 标题",
		},
		{
			name: "filename stripped of hex prefix when no h1",
			body: "no headings at all, just paragraphs",
			path: "reference/external/compute/9c6b1512-ThreadSanitizerAlgorithm.md",
			want: "ThreadSanitizerAlgorithm",
		},
		{
			name: "filename used as-is when no hex prefix",
			body: "plain doc",
			path: "diagnostics/k8s-pod-stuck.md",
			want: "k8s-pod-stuck",
		},
		{
			name: "quoted yaml title is unquoted",
			body: `---
title: "带引号的标题"
---
body`,
			path: "x.md",
			want: "带引号的标题",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := extractDocTitle(c.body, c.path)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}
