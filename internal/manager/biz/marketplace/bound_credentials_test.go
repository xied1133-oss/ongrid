package marketplace

import (
	"context"
	"sort"
	"testing"

	model "github.com/ongridio/ongrid/internal/manager/model/marketplace"
)

// TestBoundCredentialNamesForSkills covers the HLD-017 exec-injection lookup:
// given the session's active skill names, return the vault credential names
// bound to whichever installed packs ship those skills — keyed by slot OR by
// manual "extra:" association (both inject at exec).
func TestBoundCredentialNamesForSkills(t *testing.T) {
	repo := newFakeRepo()
	// Pack A ships skill "tencent_cvm_ops"; bound a declared slot + a manual extra.
	repo.rows = append(repo.rows, &model.InstalledPack{
		TenantID:         0,
		PackID:           "tencent-cvm-ops",
		CapabilitiesJSON: `{"skills":[{"name":"tencent_cvm_ops"}]}`,
		BindingsJSON:     `{"tencentcloud":"tencent-prod","extra:aws-prod":"aws-prod"}`,
	})
	// Pack B ships "terrashark" but has NO bindings — must contribute nothing.
	repo.rows = append(repo.rows, &model.InstalledPack{
		TenantID:         0,
		PackID:           "terrashark",
		CapabilitiesJSON: `{"skills":[{"name":"terrashark"}]}`,
		BindingsJSON:     ``,
	})
	uc := &Usecase{repo: repo}

	got := uc.BoundCredentialNamesForSkills(context.Background(), []string{"tencent_cvm_ops"})
	sort.Strings(got)
	want := []string{"aws-prod", "tencent-prod"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("active tencent skill: got %v, want %v", got, want)
	}

	// An active skill no installed pack ships → nothing injected.
	if names := uc.BoundCredentialNamesForSkills(context.Background(), []string{"unrelated_skill"}); len(names) != 0 {
		t.Fatalf("unrelated skill: got %v, want none", names)
	}

	// terrashark active but unbound → nothing.
	if names := uc.BoundCredentialNamesForSkills(context.Background(), []string{"terrashark"}); len(names) != 0 {
		t.Fatalf("unbound skill: got %v, want none", names)
	}

	// No active skills → nothing.
	if names := uc.BoundCredentialNamesForSkills(context.Background(), nil); names != nil {
		t.Fatalf("no skills: got %v, want nil", names)
	}
}
