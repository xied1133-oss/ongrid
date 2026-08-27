package tools

import (
	"encoding/json"
	"reflect"
	"testing"
)

// 容错参数类型的回归测试：样本来自 chat_tool_calls 里量化小模型的真实
// 畸形参数（{"item": "9"} 回声、裸标量、数字字符串），以及正常形态和
// 必须继续报错的非法值。

func TestLenientIDList(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    []uint64
		wantErr bool
	}{
		{"正常数组", `[9, 7]`, []uint64{9, 7}, false},
		{"item 对象包装+字符串", `{"item": "9"}`, []uint64{9}, false},
		{"items 对象包装", `{"items": [1, 2]}`, []uint64{1, 2}, false},
		{"多层嵌套包装", `{"item": {"items": [3]}}`, []uint64{3}, false},
		{"裸标量数字", `4`, []uint64{4}, false},
		{"裸标量字符串", `"4"`, []uint64{4}, false},
		{"数组内数字字符串", `["9", 7]`, []uint64{9, 7}, false},
		{"null", `null`, nil, false},
		{"无关对象", `{"foo": 1}`, nil, true},
		{"非数字字符串", `"abc"`, nil, true},
		{"负数", `[-1]`, nil, true},
		{"小数", `[1.5]`, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var v LenientIDList
			err := json.Unmarshal([]byte(c.raw), &v)
			if c.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr = %v", err, c.wantErr)
			}
			if !c.wantErr && !reflect.DeepEqual([]uint64(v), c.want) {
				t.Fatalf("got %v, want %v", []uint64(v), c.want)
			}
		})
	}
}

func TestLenientID(t *testing.T) {
	cases := []struct {
		raw     string
		want    uint64
		wantErr bool
	}{
		{`4`, 4, false},
		{`"4"`, 4, false},
		{`{"item": "4"}`, 4, false},
		{`"abc"`, 0, true},
		{`{}`, 0, true},
	}
	for _, c := range cases {
		var v LenientID
		err := json.Unmarshal([]byte(c.raw), &v)
		if c.wantErr != (err != nil) {
			t.Fatalf("raw %s: err = %v, wantErr = %v", c.raw, err, c.wantErr)
		}
		if !c.wantErr && uint64(v) != c.want {
			t.Fatalf("raw %s: got %d, want %d", c.raw, uint64(v), c.want)
		}
	}
}

func TestLenientStringList(t *testing.T) {
	cases := []struct {
		raw     string
		want    []string
		wantErr bool
	}{
		{`["/", "/var"]`, []string{"/", "/var"}, false},
		{`{"item": ["/", "/var"]}`, []string{"/", "/var"}, false},
		{`"/"`, []string{"/"}, false},
		{`5`, []string{"5"}, false},
		{`null`, nil, false},
		{`{"foo": 1}`, nil, true},
		{`[{}]`, nil, true},
	}
	for _, c := range cases {
		var v LenientStringList
		err := json.Unmarshal([]byte(c.raw), &v)
		if c.wantErr != (err != nil) {
			t.Fatalf("raw %s: err = %v, wantErr = %v", c.raw, err, c.wantErr)
		}
		if !c.wantErr && !reflect.DeepEqual([]string(v), c.want) {
			t.Fatalf("raw %s: got %v, want %v", c.raw, []string(v), c.want)
		}
	}
}

func TestLenientInt(t *testing.T) {
	cases := []struct {
		raw     string
		want    int
		wantErr bool
	}{
		{`1`, 1, false},
		{`"1"`, 1, false},
		{`{"item": "2"}`, 2, false},
		{`-3`, -3, false},
		{`"abc"`, 0, true},
		{`1.5`, 0, true},
	}
	for _, c := range cases {
		var v LenientInt
		err := json.Unmarshal([]byte(c.raw), &v)
		if c.wantErr != (err != nil) {
			t.Fatalf("raw %s: err = %v, wantErr = %v", c.raw, err, c.wantErr)
		}
		if !c.wantErr && int(v) != c.want {
			t.Fatalf("raw %s: got %d, want %d", c.raw, int(v), c.want)
		}
	}
}

// 端到端形态：batch 工具 args 结构体直接吃模型真实畸形参数。
func TestBatchArgsStructToleratesModelMalformations(t *testing.T) {
	var in GetIncidentDetailBatchArgs
	if err := json.Unmarshal([]byte(`{"incident_ids": {"item": "9"}}`), &in); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual([]uint64(in.IncidentIDs), []uint64{9}) {
		t.Fatalf("IncidentIDs = %v, want [9]", []uint64(in.IncidentIDs))
	}

	var du duSummaryArgs
	if err := json.Unmarshal([]byte(`{"device_id": "4", "paths": {"item": ["/", "/var"]}, "depth": "1"}`), &du); err != nil {
		t.Fatalf("unmarshal du: %v", err)
	}
	if uint64(du.DeviceID) != 4 || int(du.Depth) != 1 || !reflect.DeepEqual([]string(du.Paths), []string{"/", "/var"}) {
		t.Fatalf("du = %+v", du)
	}
}
