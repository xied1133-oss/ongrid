package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ongridio/ongrid/internal/skill"
)

func init() { skill.Register(&TailFile{}) }

// TailFile returns the last N lines of a file, similar to `tail -n`. Safe:
// read-only, but enforces an absolute-path + no-traversal policy so AI
// agents can't scribble down "../../etc/shadow" at the param.
type TailFile struct{}

// Metadata returns the framework-visible spec for tail_file.
func (TailFile) Metadata() skill.Metadata {
	return skill.Metadata{
		Key:         "host_tail_file",
		Name:        "文件尾部读取",
		Description: "读取文件最后 N 行（类似 tail -n）",
		Class:       skill.ClassSafe,
		Category:    "filesystem",
		Params: skill.ParamSchema{
			{Name: "path", Param: skill.Param{
				Type: "string", Required: true,
				Desc: "文件绝对路径，必须以 / 开头且不含 ..",
			}},
			{Name: "lines", Param: skill.Param{
				Type: "int", Default: 100,
				Desc: "返回行数，默认 100",
			}},
			{Name: "max_bytes", Param: skill.Param{
				Type: "int", Default: 1048576,
				Desc: "最多读取的尾部字节数，默认 1MiB",
			}},
		},
		ResultPreview: "{lines, total_lines_returned, file_size, truncated, error?}",
	}
}

type tailFileParams struct {
	Path     string `json:"path"`
	Lines    int    `json:"lines"`
	MaxBytes int64  `json:"max_bytes"`
}

type tailFileResult struct {
	Lines              []string `json:"lines"`
	TotalLinesReturned int      `json:"total_lines_returned"`
	FileSize           int64    `json:"file_size"`
	Truncated          bool     `json:"truncated"`
	Error              string   `json:"error,omitempty"`
}

// Execute opens the file, seeks to FileSize-MaxBytes when the file is
// larger than the budget (truncated=true), and returns the last N lines.
func (TailFile) Execute(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	var p tailFileParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, fmt.Errorf("tail_file: decode params: %w", err)
		}
	}
	if p.Path == "" {
		return nil, fmt.Errorf("tail_file: path required")
	}
	if !filepath.IsAbs(p.Path) {
		return nil, fmt.Errorf("tail_file: path must be absolute")
	}
	if strings.Contains(p.Path, "..") {
		return nil, fmt.Errorf("tail_file: path must not contain ..")
	}
	if p.Lines <= 0 {
		p.Lines = 100
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 1 << 20 // 1 MiB
	}

	res := tailFileResult{Lines: []string{}}

	f, err := os.Open(p.Path)
	if err != nil {
		res.Error = err.Error()
		return json.Marshal(res)
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		res.Error = err.Error()
		return json.Marshal(res)
	}
	res.FileSize = st.Size()

	var data []byte
	if st.Size() > p.MaxBytes {
		res.Truncated = true
		if _, err := f.Seek(st.Size()-p.MaxBytes, io.SeekStart); err != nil {
			res.Error = err.Error()
			return json.Marshal(res)
		}
	}
	data, err = io.ReadAll(f)
	if err != nil {
		res.Error = err.Error()
		return json.Marshal(res)
	}

	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if res.Truncated && len(all) > 0 {
		// First line may be a partial line from the seek midpoint; drop
		// it so we never claim half a log line as an entry.
		all = all[1:]
	}
	if len(all) > p.Lines {
		all = all[len(all)-p.Lines:]
	}
	if len(all) == 1 && all[0] == "" {
		all = []string{}
	}
	res.Lines = all
	res.TotalLinesReturned = len(all)
	return json.Marshal(res)
}
