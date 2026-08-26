package qdrantx

import (
	"reflect"
	"testing"
)

func TestBuildFilter_Empty(t *testing.T) {
	if buildFilter(nil) != nil {
		t.Errorf("nil → nil")
	}
	if buildFilter(map[string]any{}) != nil {
		t.Errorf("empty → nil")
	}
}

func TestBuildFilter_StringValue(t *testing.T) {
	got := buildFilter(map[string]any{"category": "网络"})
	want := map[string]any{"must": []map[string]any{
		{"key": "category", "match": map[string]any{"value": "网络"}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestBuildFilter_StringSliceBecomesAny(t *testing.T) {
	got := buildFilter(map[string]any{"tags": []string{"dns", "tls"}})
	must, ok := got["must"].([]map[string]any)
	if !ok || len(must) != 1 {
		t.Fatalf("got %+v", got)
	}
	cond := must[0]
	if cond["key"] != "tags" {
		t.Errorf("key=%v", cond["key"])
	}
	match, _ := cond["match"].(map[string]any)
	anyList, _ := match["any"].([]any)
	if len(anyList) != 2 || anyList[0] != "dns" || anyList[1] != "tls" {
		t.Errorf("any=%+v", anyList)
	}
}

func TestBuildFilter_PrefixMatch(t *testing.T) {
	got := buildFilter(map[string]any{"path": PrefixMatch{Prefix: "网络/"}})
	must, _ := got["must"].([]map[string]any)
	if len(must) != 1 {
		t.Fatalf("got %+v", got)
	}
	cond := must[0]
	match, _ := cond["match"].(map[string]any)
	if match["text"] != "网络/" {
		t.Errorf("want text=网络/, got %v", match)
	}
}

func TestBuildFilter_EmptyPrefixSkipped(t *testing.T) {
	got := buildFilter(map[string]any{"path": PrefixMatch{Prefix: ""}, "tag": "x"})
	must, _ := got["must"].([]map[string]any)
	if len(must) != 1 || must[0]["key"] != "tag" {
		t.Errorf("empty prefix should be skipped, got %+v", got)
	}
}

func TestBuildFilter_EmptySliceSkipped(t *testing.T) {
	got := buildFilter(map[string]any{"tags": []string{}, "category": "网络"})
	must, _ := got["must"].([]map[string]any)
	if len(must) != 1 {
		t.Errorf("empty slice should be skipped, got %+v", got)
	}
	if must[0]["key"] != "category" {
		t.Errorf("expected category-only, got %+v", must)
	}
}
