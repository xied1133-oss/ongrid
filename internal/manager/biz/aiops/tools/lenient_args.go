package tools

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// 容错参数类型。量化小模型（如 MiniMax-M3-AWQ-INT4）调工具时反复出现三类
// 参数畸形（chat_tool_calls 有实证）：
//  1. 把 schema 的 items 描述符回声成数据：{"incident_ids": {"item": "9"}}；
//  2. 把数组传成裸标量：{"device_ids": 4}；
//  3. 把数字写成字符串：{"device_id": "4", "depth": "1"}。
// 严格 json.Unmarshal 遇这些直接整轮 tool call 报废（bad args），agent 要
// 额外花一轮自救。以下类型在 unmarshal 阶段统一归一化，让工具对畸形参数
// 保持可用；真正非法的值（负数/小数/非数字串/无关对象）仍然报错。

// unwrapSchemaEcho 剥掉 LLM 把 schema 关键字当数据回声出的对象包装：
// {"item": X} / {"items": X} → X；循环剥以兼容多层嵌套。
// 非单键对象或键不匹配时原样返回。
func unwrapSchemaEcho(v any) any {
	for {
		m, ok := v.(map[string]any)
		if !ok || len(m) != 1 {
			return v
		}
		inner, hit := m["item"]
		if !hit {
			inner, hit = m["items"]
		}
		if !hit {
			return v
		}
		v = inner
	}
}

// coerceUint64 接受 JSON 数字（整数值的 float64）与数字字符串，其余报错。
func coerceUint64(v any) (uint64, error) {
	switch n := v.(type) {
	case float64:
		if n < 0 || n > math.MaxUint64 || n != math.Trunc(n) {
			return 0, fmt.Errorf("not an unsigned integer: %v", v)
		}
		return uint64(n), nil
	case string:
		u, err := strconv.ParseUint(strings.TrimSpace(n), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("not an unsigned integer: %q", n)
		}
		return u, nil
	default:
		return 0, fmt.Errorf("not an unsigned integer: %v", v)
	}
}

// coerceString 接受字符串与数字（转十进制字面），其余报错。
func coerceString(v any) (string, error) {
	switch s := v.(type) {
	case string:
		return s, nil
	case float64:
		if s == math.Trunc(s) {
			return strconv.FormatInt(int64(s), 10), nil
		}
		return strconv.FormatFloat(s, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("not a string: %v", v)
	}
}

// LenientID 是容忍数字字符串/对象包装的单个 uint64 参数。
type LenientID uint64

// UnmarshalJSON 实现容错解析；null 保持零值。
func (v *LenientID) UnmarshalJSON(raw []byte) error {
	var boxed any
	if err := json.Unmarshal(raw, &boxed); err != nil {
		return err
	}
	if boxed == nil {
		return nil
	}
	u, err := coerceUint64(unwrapSchemaEcho(boxed))
	if err != nil {
		return err
	}
	*v = LenientID(u)
	return nil
}

// LenientIDList 是容忍对象包装/裸标量/数字字符串的 uint64 数组参数。
type LenientIDList []uint64

// UnmarshalJSON 归一化规则：{"item(s)": X} → X；裸标量 → [标量]；
// 数组元素逐个按 coerceUint64 解析。
func (v *LenientIDList) UnmarshalJSON(raw []byte) error {
	var boxed any
	if err := json.Unmarshal(raw, &boxed); err != nil {
		return err
	}
	if boxed == nil {
		return nil
	}
	if list, ok := unwrapSchemaEcho(boxed).([]any); ok {
		out := make([]uint64, 0, len(list))
		for _, e := range list {
			u, err := coerceUint64(e)
			if err != nil {
				return err
			}
			out = append(out, u)
		}
		*v = out
		return nil
	}
	u, err := coerceUint64(unwrapSchemaEcho(boxed))
	if err != nil {
		return err
	}
	*v = []uint64{u}
	return nil
}

// LenientStringList 是容忍对象包装/裸标量的字符串数组参数（paths 等）。
type LenientStringList []string

// UnmarshalJSON 归一化规则同 LenientIDList，元素按 coerceString 解析。
func (v *LenientStringList) UnmarshalJSON(raw []byte) error {
	var boxed any
	if err := json.Unmarshal(raw, &boxed); err != nil {
		return err
	}
	if boxed == nil {
		return nil
	}
	if list, ok := unwrapSchemaEcho(boxed).([]any); ok {
		out := make([]string, 0, len(list))
		for _, e := range list {
			s, err := coerceString(e)
			if err != nil {
				return err
			}
			out = append(out, s)
		}
		*v = out
		return nil
	}
	s, err := coerceString(unwrapSchemaEcho(boxed))
	if err != nil {
		return err
	}
	*v = []string{s}
	return nil
}

// LenientInt 是容忍数字字符串/对象包装的 int 参数（depth/top_n 等）。
type LenientInt int

// UnmarshalJSON 实现容错解析；null 保持零值。
func (v *LenientInt) UnmarshalJSON(raw []byte) error {
	var boxed any
	if err := json.Unmarshal(raw, &boxed); err != nil {
		return err
	}
	if boxed == nil {
		return nil
	}
	switch n := unwrapSchemaEcho(boxed).(type) {
	case float64:
		if n != math.Trunc(n) || n < math.MinInt64 || n > math.MaxInt64 {
			return fmt.Errorf("not an integer: %v", boxed)
		}
		*v = LenientInt(int(n))
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return fmt.Errorf("not an integer: %q", n)
		}
		*v = LenientInt(i)
	default:
		return fmt.Errorf("not an integer: %v", boxed)
	}
	return nil
}
