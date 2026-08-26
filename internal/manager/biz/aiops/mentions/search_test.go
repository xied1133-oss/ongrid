package mentions

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubLogQuerier struct {
	values []string
	err    error
}

func (s *stubLogQuerier) LabelValues(_ context.Context, name string, _, _ time.Time) ([]string, error) {
	if name != "filename" {
		return nil, errors.New("unexpected label")
	}
	return s.values, s.err
}

func TestSearchFiles_FilterByTerm(t *testing.T) {
	s := &Searcher{
		logClient: &stubLogQuerier{values: []string{
			"/var/log/nginx/access.log",
			"/var/log/syslog",
			"/opt/app/error.log",
		}},
	}
	got, err := s.searchFiles(context.Background(), "log", 10)
	if err != nil {
		t.Fatalf("searchFiles: %v", err)
	}
	if len(got) != 3 {
		t.Errorf("expected 3 files, got %d", len(got))
	}
	got, _ = s.searchFiles(context.Background(), "nginx", 10)
	if len(got) != 1 || got[0].ID != "/var/log/nginx/access.log" {
		t.Errorf("nginx filter mismatch: %+v", got)
	}
}

func TestSearch_FilterRespected(t *testing.T) {
	s := &Searcher{logClient: &stubLogQuerier{values: []string{"/a.log", "/b.log"}}}
	items, err := s.Search(context.Background(), Query{Term: "log", Filter: TypeFile})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("expected 2 files, got %+v", items)
	}
	for _, it := range items {
		if it.Type != TypeFile {
			t.Errorf("filter leak: %v", it)
		}
	}
}

func TestSearch_NilDepsNoError(t *testing.T) {
	s := &Searcher{}
	items, err := s.Search(context.Background(), Query{Term: "anything"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected empty; got %+v", items)
	}
}

func TestResolve_FileBullet(t *testing.T) {
	s := &Searcher{}
	out := s.Resolve(context.Background(), []Mention{{Type: TypeFile, ID: "/var/log/syslog"}})
	if len(out) != 1 || out[0] != "- log file /var/log/syslog" {
		t.Errorf("file bullet = %+v", out)
	}
}

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{
		"":     false,
		"123":  true,
		"12a":  false,
		"0":    true,
		"-12":  false,
	}
	for in, want := range cases {
		if got := isAllDigits(in); got != want {
			t.Errorf("isAllDigits(%q) = %t, want %t", in, got, want)
		}
	}
}
