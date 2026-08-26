package cmdpolicy

import (
	"strings"
	"testing"
)

func TestSplitPipes_Basic(t *testing.T) {
	cases := []struct {
		in   string
		want [][]string
	}{
		{"ps", [][]string{{"ps"}}},
		{"ps aux", [][]string{{"ps", "aux"}}},
		{"ps aux | head -10", [][]string{{"ps", "aux"}, {"head", "-10"}}},
		{"ls /var | grep log | wc -l", [][]string{{"ls", "/var"}, {"grep", "log"}, {"wc", "-l"}}},
		{`echo "hello world"`, [][]string{{"echo", "hello world"}}},
		{`echo 'a b c'`, [][]string{{"echo", "a b c"}}},
		{`echo a\ b`, [][]string{{"echo", "a b"}}},
	}
	for _, c := range cases {
		got, err := SplitPipes(c.in)
		if err != nil {
			t.Errorf("SplitPipes(%q) err: %v", c.in, err)
			continue
		}
		if !sliceEq(got, c.want) {
			t.Errorf("SplitPipes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSplitPipes_ForbiddenChars(t *testing.T) {
	bad := []string{
		"echo > /tmp/x",
		"echo >> /tmp/x",
		"cat < /tmp/x",
		"echo $(date)",
		"echo `whoami`",
		"echo a; echo b",
		"echo a && echo b",
		"echo a || echo b",
		"echo a &",
		"diff <(ls) <(ls)",
		"echo ${HOME}",
		"echo |",      // empty trailing segment
		"| ps",        // empty leading segment
		`echo "unterm`, // unterminated quote
		`echo \`,       // trailing backslash
	}
	for _, b := range bad {
		if _, err := SplitPipes(b); err == nil {
			t.Errorf("SplitPipes(%q) accepted, expected error", b)
		}
	}
}

func TestSplitPipes_QuotedPipeIsLiteral(t *testing.T) {
	// A pipe inside quotes must NOT be treated as a separator.
	got, err := SplitPipes(`grep "a|b" /etc/hosts`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0][1] != "a|b" {
		t.Errorf("got %v, want single segment with literal a|b", got)
	}
}

func TestParseSegment_Basic(t *testing.T) {
	got, err := ParseSegment("iptables -L -n")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strSliceEq(got, []string{"iptables", "-L", "-n"}) {
		t.Errorf("got %v", got)
	}
}

func TestParseSegment_RejectsPipe(t *testing.T) {
	if _, err := ParseSegment("ps | head"); err == nil {
		t.Errorf("expected error for pipe in single segment")
	}
}

func TestSplitPipes_DollarLiteralOK(t *testing.T) {
	// $FOO is allowed as a literal token (we never expand it).
	got, err := SplitPipes("echo $FOO")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0][1] != "$FOO" {
		t.Errorf("dollar token = %q, want $FOO", got[0][1])
	}
}

func TestSplitPipes_DoubleQuotedEscapes(t *testing.T) {
	got, err := SplitPipes(`echo "a\"b"`)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got[0][1] != `a"b` {
		t.Errorf("got %q, want a\"b", got[0][1])
	}
}

func TestSplitPipes_RejectsCommandSubstInQuotes(t *testing.T) {
	if _, err := SplitPipes(`echo "$(date)"`); err == nil || !strings.Contains(err.Error(), "command substitution") {
		t.Errorf("expected substitution error, got %v", err)
	}
}

// helpers
func sliceEq(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strSliceEq(a[i], b[i]) {
			return false
		}
	}
	return true
}

func strSliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
