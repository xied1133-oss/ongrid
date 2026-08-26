package tunnel

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestFindLargeFilesRequest_BatchJSON ensures the batch wire shape
// round-trips: paths is an array, top_n / min_size_bytes / exclude_paths
// preserve their semantics.
func TestFindLargeFilesRequest_BatchJSON(t *testing.T) {
	in := FindLargeFilesRequest{
		Paths:        []string{"/var/log", "/opt", "/home"},
		TopN:         10,
		MinSizeBytes: 2 * 1024 * 1024,
		ExcludePaths: []string{"/proc", "/sys"},
	}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"paths":["/var/log","/opt","/home"]`) {
		t.Errorf("paths not array-shaped in JSON: %s", b)
	}
	var out FindLargeFilesRequest
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(out.Paths) != 3 || out.TopN != 10 || out.MinSizeBytes != 2*1024*1024 {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

// TestFindLargeFilesResponse_PartialSuccess ensures success and error
// rows can coexist in Results — and that the wire form strips empty
// fields via omitempty (Files nil on the error row, Error empty on the
// success row).
func TestFindLargeFilesResponse_PartialSuccess(t *testing.T) {
	mtime := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	resp := FindLargeFilesResponse{
		Results: []FindLargeFilesResultEntry{
			{Path: "/var/log", ScannedPath: "/var/log", Files: []HostFileInfo{{Path: "/var/log/messages", SizeBytes: 100, SizeHuman: "100 B", Mtime: mtime, Owner: "root"}}},
			{Path: "/etc", Error: "sandbox: not in allow-list"},
		},
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, `"results":`) {
		t.Errorf("missing results key: %s", s)
	}
	if !strings.Contains(s, `"error":"sandbox: not in allow-list"`) {
		t.Errorf("error string lost: %s", s)
	}
	// Round-trip and verify per-row error is preserved.
	var got FindLargeFilesResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Results) != 2 {
		t.Fatalf("Results len = %d", len(got.Results))
	}
	if got.Results[0].Error != "" {
		t.Errorf("entry 0 error should be empty, got %q", got.Results[0].Error)
	}
	if got.Results[1].Error == "" {
		t.Errorf("entry 1 error lost in round-trip")
	}
	if len(got.Results[1].Files) != 0 {
		t.Errorf("entry 1 should have no files, got %+v", got.Results[1].Files)
	}
}

func TestDuSummaryRequest_BatchJSON(t *testing.T) {
	in := DuSummaryRequest{Paths: []string{"/var", "/opt"}, Depth: 2}
	b, _ := json.Marshal(in)
	if !strings.Contains(string(b), `"paths":["/var","/opt"]`) {
		t.Errorf("paths not array-shaped: %s", b)
	}
	var out DuSummaryRequest
	_ = json.Unmarshal(b, &out)
	if len(out.Paths) != 2 || out.Depth != 2 {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestDuSummaryResponse_PartialSuccess(t *testing.T) {
	resp := DuSummaryResponse{
		Results: []DuSummaryResultEntry{
			{Path: "/var", Subpaths: []HostDuEntry{{Subpath: "/var/log", SizeBytes: 1024, SizeHuman: "1.0 KiB"}}, TotalSizeBytes: 1024, TotalSizeHuman: "1.0 KiB"},
			{Path: "/proc", Error: "scan never finishes"},
		},
	}
	b, _ := json.Marshal(resp)
	var got DuSummaryResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Results[0].TotalSizeBytes != 1024 || got.Results[1].Error == "" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

func TestStatFileRequest_BatchJSON(t *testing.T) {
	in := StatFileRequest{Paths: []string{"/etc/passwd", "/var/log/syslog"}}
	b, _ := json.Marshal(in)
	if !strings.Contains(string(b), `"paths":["/etc/passwd","/var/log/syslog"]`) {
		t.Errorf("paths not array-shaped: %s", b)
	}
	var out StatFileRequest
	_ = json.Unmarshal(b, &out)
	if len(out.Paths) != 2 {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}

func TestStatFileResponse_PartialSuccess(t *testing.T) {
	mtime := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	resp := StatFileResponse{
		Results: []StatFileResultEntry{
			{Path: "/etc/passwd", Type: "file", SizeBytes: 100, SizeHuman: "100 B", Mtime: mtime, Atime: mtime, Mode: "0644", Owner: "root", Group: "root"},
			{Path: "/no/such/file", Error: "no such file"},
		},
	}
	b, _ := json.Marshal(resp)
	var got StatFileResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Results[0].Type != "file" || got.Results[1].Error == "" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Error row should NOT have Type populated (omitempty + zero-value).
	if got.Results[1].Type != "" {
		t.Errorf("error row Type should be empty, got %q", got.Results[1].Type)
	}
}
