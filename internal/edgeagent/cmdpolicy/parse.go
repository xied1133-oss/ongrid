package cmdpolicy

import (
	"errors"
	"fmt"
	"strings"
)

// parse.go implements a minimal POSIX-flavoured shell tokenizer that
// supports ONLY the subset cmdpolicy guarantees: simple commands with
// optional pipes, double / single-quoted strings, and \-escapes. Every
// other shell feature is rejected at the source — we never feed the
// resulting argv into a real shell, and we want the LLM to never even
// pretend it can use them.
//
// Forbidden (always error):
//
//   - redirects: > >> < <<
//   - heredoc / herestring: << <<<
//   - command list / background: ; && || &
//   - command substitution: $( ) ` `
//   - process substitution: <(  >(
//   - subshell: ( ) at the top level
//   - variable expansion past the bare token (we do not expand
//     anything; $FOO is permitted only as a literal token because
//     exec.Command will receive it verbatim — but $() and ${} are
//     blocked because they signal substitution intent)
//
// The tokenizer is intentionally hand-written (~120 lines) so we don't
// have to import google/shlex or a full shell parser.

// SplitPipes splits cmd into pipeline segments. Each segment's
// returned argv is already tokenised. Returns an error when any
// forbidden metacharacter is encountered or when the input parses to
// zero segments / a segment with zero argv.
func SplitPipes(cmd string) ([][]string, error) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return nil, errors.New("cmdpolicy: empty command")
	}
	tokens, breaks, err := tokenize(cmd)
	if err != nil {
		return nil, err
	}
	// breaks is a parallel slice of bools: true at index i means token
	// i is the pipe separator (the literal "|" produced by tokenize)
	// rather than a regular argv token.
	var segments [][]string
	var cur []string
	for i, t := range tokens {
		if breaks[i] {
			if len(cur) == 0 {
				return nil, errors.New("cmdpolicy: empty pipe segment")
			}
			segments = append(segments, cur)
			cur = nil
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) == 0 {
		return nil, errors.New("cmdpolicy: empty trailing segment")
	}
	segments = append(segments, cur)
	if len(segments) == 0 {
		return nil, errors.New("cmdpolicy: no segments parsed")
	}
	return segments, nil
}

// ParseSegment tokenises one shell-like command string into its argv.
// Same forbidden-character rules as SplitPipes (the underlying
// tokenizer rejects pipes too, so callers wanting pipe support must go
// through SplitPipes; ParseSegment is here for the rare case where the
// caller knows the input is a single segment, e.g. tests).
func ParseSegment(seg string) ([]string, error) {
	seg = strings.TrimSpace(seg)
	if seg == "" {
		return nil, errors.New("cmdpolicy: empty segment")
	}
	tokens, breaks, err := tokenize(seg)
	if err != nil {
		return nil, err
	}
	for _, b := range breaks {
		if b {
			return nil, errors.New("cmdpolicy: pipe encountered in single segment")
		}
	}
	if len(tokens) == 0 {
		return nil, errors.New("cmdpolicy: no tokens parsed")
	}
	return tokens, nil
}

// tokenize is the core scanner. Returns parallel (tokens, breaks)
// slices: breaks[i]==true means the token at i is a pipe separator
// (its string value is "|"); breaks[i]==false means a regular argv
// token. Any forbidden metacharacter or unterminated quote returns
// an error.
func tokenize(in string) ([]string, []bool, error) {
	var (
		tokens []string
		breaks []bool
		buf    strings.Builder
		// inToken tracks whether buf currently holds an in-progress
		// (unquoted) token; it lets us emit empty quoted strings as
		// a token (e.g. `""` is a valid argv element).
		inToken bool
	)

	flush := func() {
		if inToken {
			tokens = append(tokens, buf.String())
			breaks = append(breaks, false)
			buf.Reset()
			inToken = false
		}
	}

	runes := []rune(in)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		switch r {
		case ' ', '\t', '\n':
			flush()
		case '|':
			// "||" is forbidden (logical OR). A bare "|" is the pipe
			// separator we welcome.
			if i+1 < len(runes) && runes[i+1] == '|' {
				return nil, nil, errors.New("cmdpolicy: '||' is forbidden")
			}
			flush()
			tokens = append(tokens, "|")
			breaks = append(breaks, true)
		case '&':
			// "&&" forbidden, single "&" forbidden (background).
			return nil, nil, errors.New("cmdpolicy: '&' is forbidden")
		case ';':
			return nil, nil, errors.New("cmdpolicy: ';' is forbidden")
		case '>', '<':
			// Any redirect is forbidden — even the read-only ones
			// (we don't want the LLM to think tee / cat > file works).
			return nil, nil, fmt.Errorf("cmdpolicy: redirect %q is forbidden", string(r))
		case '`':
			return nil, nil, errors.New("cmdpolicy: backtick command substitution is forbidden")
		case '$':
			// $( ) and ${...} are forbidden; bare "$VAR" passes
			// through as a literal token (it is never expanded
			// because we never invoke a shell).
			if i+1 < len(runes) && runes[i+1] == '(' {
				return nil, nil, errors.New("cmdpolicy: '$(' command substitution is forbidden")
			}
			if i+1 < len(runes) && runes[i+1] == '{' {
				return nil, nil, errors.New("cmdpolicy: '${' parameter expansion is forbidden")
			}
			buf.WriteRune(r)
			inToken = true
		case '(', ')':
			return nil, nil, fmt.Errorf("cmdpolicy: %q is forbidden (subshell / process substitution)", string(r))
		case '\\':
			// \-escape: next rune is taken literally. Reject if it
			// terminates the input (incomplete escape).
			if i+1 >= len(runes) {
				return nil, nil, errors.New("cmdpolicy: trailing backslash")
			}
			i++
			buf.WriteRune(runes[i])
			inToken = true
		case '"':
			s, n, err := readQuoted(runes, i+1, '"')
			if err != nil {
				return nil, nil, err
			}
			buf.WriteString(s)
			inToken = true
			i = n
		case '\'':
			s, n, err := readQuoted(runes, i+1, '\'')
			if err != nil {
				return nil, nil, err
			}
			buf.WriteString(s)
			inToken = true
			i = n
		default:
			buf.WriteRune(r)
			inToken = true
		}
	}
	flush()
	return tokens, breaks, nil
}

// readQuoted reads from runes[start:] until it finds an unescaped
// terminator. Returns the unquoted body, the index of the closing
// quote, and an error if the terminator never arrives. Inside double
// quotes \\ and \" are honoured; inside single quotes everything is
// literal until the next single quote (POSIX sh single-quote rule).
func readQuoted(runes []rune, start int, term rune) (string, int, error) {
	var b strings.Builder
	for i := start; i < len(runes); i++ {
		r := runes[i]
		if r == term {
			return b.String(), i, nil
		}
		if term == '"' && r == '\\' && i+1 < len(runes) {
			next := runes[i+1]
			// Inside double quotes only \" \\ \$ \` are honoured per
			// POSIX. Other backslashes pass through literally.
			switch next {
			case '"', '\\', '$', '`':
				b.WriteRune(next)
				i++
				continue
			}
			b.WriteRune(r)
			continue
		}
		// Reject command substitution / parameter expansion inside
		// double quotes too — same reasoning as the unquoted path.
		if term == '"' && r == '$' && i+1 < len(runes) {
			next := runes[i+1]
			if next == '(' || next == '{' {
				return "", 0, errors.New("cmdpolicy: command substitution forbidden inside string")
			}
		}
		if term == '"' && r == '`' {
			return "", 0, errors.New("cmdpolicy: backtick forbidden inside string")
		}
		b.WriteRune(r)
	}
	return "", 0, fmt.Errorf("cmdpolicy: unterminated quoted string (%q)", string(term))
}
