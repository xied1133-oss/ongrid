package logquery

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const (
	maxQueryLogQLBytes = 16 << 10
	// query_logql's established public schema allows up to 5000 rows. ES
	// adapters keep their safer 1000-row page limit; the selected-backend
	// service follows opaque cursors to satisfy this outer limit.
	MaxQueryLogQLLimit = 5000
)

var logQLLabelMatcher = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.]*)\s*(=~|!~|!=|=)\s*("(?:\\.|[^"\\])*")$`)

// CompileLogQLSearch translates the log-search subset of LogQL into the
// backend-neutral request used by Elasticsearch. Native Loki queries do not
// pass through this compiler and therefore retain the full LogQL language.
//
// Supported syntax intentionally stays small and predictable:
//
//   - an allowlisted stream selector with =, !=, =~ or !~ matchers;
//   - line filters |=, !=, |~ and !~;
//   - literal alternation in regex filters, for example
//     |~ "(?i)error|panic|fatal".
//
// Parser stages, unwrap and metric expressions are rejected instead of being
// approximated with Elasticsearch DSL that would change their meaning.
func CompileLogQLSearch(opts QueryRangeOptions) (SearchRequest, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchRequest{}, errors.New("logquery: query is empty")
	}
	if len(query) > maxQueryLogQLBytes {
		return SearchRequest{}, fmt.Errorf("logquery: LogQL query exceeds %d bytes", maxQueryLogQLBytes)
	}
	if opts.Step > 0 {
		return SearchRequest{}, errors.New("logquery: LogQL metric queries are only available when Loki is selected")
	}
	if !strings.HasPrefix(query, "{") {
		return SearchRequest{}, errors.New("logquery: metric LogQL and expressions without a leading stream selector are only available when Loki is selected")
	}
	selector, rest, err := splitLogQLSelector(query)
	if err != nil {
		return SearchRequest{}, err
	}
	filters, err := compileLogQLSelector(selector)
	if err != nil {
		return SearchRequest{}, err
	}
	keywords, err := compileLogQLLineFilters(rest)
	if err != nil {
		return SearchRequest{}, err
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = MaxSearchLimit
	}
	if limit > MaxQueryLogQLLimit {
		return SearchRequest{}, fmt.Errorf("logquery: Elasticsearch query_logql limit must not exceed %d", MaxQueryLogQLLimit)
	}
	direction := SortDirection(opts.Direction)
	if direction == "" {
		direction = SortBackward
	}
	req := SearchRequest{
		Start:     opts.Start,
		End:       opts.End,
		Keywords:  keywords,
		Filters:   filters,
		Limit:     min(limit, MaxSearchLimit),
		Direction: direction,
	}
	if err := req.NormalizeAndValidate(); err != nil {
		return SearchRequest{}, err
	}
	return req, nil
}

func splitLogQLSelector(query string) (string, string, error) {
	if !strings.HasPrefix(query, "{") {
		return "", "", errors.New("logquery: Elasticsearch query_logql must start with a stream selector")
	}
	inQuote := false
	escaped := false
	for i := 1; i < len(query); i++ {
		switch query[i] {
		case '\\':
			if inQuote {
				escaped = !escaped
			}
		case '"':
			if !escaped {
				inQuote = !inQuote
			}
			escaped = false
		case '}':
			if !inQuote {
				return query[1:i], strings.TrimSpace(query[i+1:]), nil
			}
		default:
			escaped = false
		}
	}
	return "", "", errors.New("logquery: invalid LogQL stream selector")
}

func compileLogQLSelector(selector string) ([]FieldFilter, error) {
	parts, err := splitLogQLMatchers(selector)
	if err != nil {
		return nil, err
	}
	filters := make([]FieldFilter, 0, len(parts))
	for _, part := range parts {
		match := logQLLabelMatcher.FindStringSubmatch(strings.TrimSpace(part))
		if len(match) != 4 {
			return nil, fmt.Errorf("logquery: unsupported LogQL label matcher %q", strings.TrimSpace(part))
		}
		field, ok := portableLogQLField(match[1])
		if !ok {
			return nil, fmt.Errorf("logquery: LogQL label %q is not available on the selected Elasticsearch backend", match[1])
		}
		value, err := strconv.Unquote(match[3])
		if err != nil {
			return nil, fmt.Errorf("logquery: invalid LogQL matcher value: %w", err)
		}
		filter := FieldFilter{Field: field}
		switch match[2] {
		case "=":
			filter.Operator, filter.Values = FilterEqual, []string{value}
		case "!=":
			filter.Operator, filter.Values = FilterNotEqual, []string{value}
		case "=~", "!~":
			if strings.HasPrefix(strings.TrimSpace(value), "(?i)") {
				return nil, fmt.Errorf("logquery: case-insensitive selector regex for %q is only available when Loki is selected", match[1])
			}
			if isLogQLMatchAllRegex(value) {
				if match[2] == "!~" {
					return nil, fmt.Errorf("logquery: negative match-all selector for %q is only available when Loki is selected", match[1])
				}
				filter.Operator = FilterExists
				filters = append(filters, filter)
				continue
			}
			if prefix, ok := literalLogQLPrefix(value); ok {
				if match[2] == "!~" {
					return nil, fmt.Errorf("logquery: negative prefix selector for %q is only available when Loki is selected", match[1])
				}
				filter.Operator = FilterPrefix
				filter.Values = []string{prefix}
				filters = append(filters, filter)
				continue
			}
			values, err := literalLogQLRegexValues(value)
			if err != nil {
				return nil, fmt.Errorf("logquery: selector %s for %q: %w", match[2], match[1], err)
			}
			if match[2] == "=~" {
				filter.Operator = FilterIn
			} else {
				filter.Operator = FilterNotEqual
			}
			filter.Values = values
		}
		filters = append(filters, filter)
	}
	return filters, nil
}

func isLogQLMatchAllRegex(pattern string) bool {
	pattern = strings.TrimSpace(pattern)
	if strings.HasPrefix(pattern, "^") && strings.HasSuffix(pattern, "$") && len(pattern) >= 2 {
		pattern = strings.TrimSpace(pattern[1 : len(pattern)-1])
	}
	return pattern == ".+" || pattern == ".*"
}

// literalLogQLPrefix recognizes the bounded prefix shapes produced by the
// legacy alert presets. It deliberately does not accept arbitrary regex: the
// backend-neutral contract exposes a safe prefix operator rather than raw
// Elasticsearch or LogQL expressions.
func literalLogQLPrefix(pattern string) (string, bool) {
	pattern = strings.TrimSpace(pattern)
	if strings.HasPrefix(pattern, "^") && strings.HasSuffix(pattern, "$") && len(pattern) >= 2 {
		pattern = strings.TrimSpace(pattern[1 : len(pattern)-1])
	}
	for _, suffix := range []string{"(:.*)?", "(:.+)?", ".*", ".+"} {
		if !strings.HasSuffix(pattern, suffix) {
			continue
		}
		prefix := strings.TrimSpace(strings.TrimSuffix(pattern, suffix))
		if prefix == "" || strings.ContainsAny(prefix, `.\\[]{}()*+?^$|`) {
			return "", false
		}
		return prefix, true
	}
	return "", false
}

func splitLogQLMatchers(selector string) ([]string, error) {
	if strings.TrimSpace(selector) == "" {
		return nil, nil
	}
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	for i := 0; i < len(selector); i++ {
		switch selector[i] {
		case '\\':
			if inQuote {
				escaped = !escaped
			}
		case '"':
			if !escaped {
				inQuote = !inQuote
			}
			escaped = false
		case ',':
			if !inQuote {
				part := strings.TrimSpace(selector[start:i])
				if part == "" {
					return nil, errors.New("logquery: empty LogQL label matcher")
				}
				parts = append(parts, part)
				start = i + 1
			}
		default:
			escaped = false
		}
	}
	if inQuote {
		return nil, errors.New("logquery: unterminated LogQL matcher string")
	}
	part := strings.TrimSpace(selector[start:])
	if part == "" {
		return nil, errors.New("logquery: empty LogQL label matcher")
	}
	return append(parts, part), nil
}

func portableLogQLField(label string) (string, bool) {
	switch label {
	case "ongrid_source":
		// Loki and the product search API expose the same dimension under
		// different canonical names.
		return "source_id", true
	case "filename":
		return "file", true
	default:
		_, ok := LookupField(label)
		return label, ok && label != "message"
	}
}

func compileLogQLLineFilters(rest string) (Keywords, error) {
	var exactInclude []string
	var regexInclude []string
	var exclude []string
	for strings.TrimSpace(rest) != "" {
		rest = strings.TrimSpace(rest)
		op := ""
		for _, candidate := range []string{"|=", "!=", "|~", "!~"} {
			if strings.HasPrefix(rest, candidate) {
				op = candidate
				rest = strings.TrimSpace(rest[len(candidate):])
				break
			}
		}
		if op == "" {
			return Keywords{}, errors.New("logquery: parser stages, unwrap, and metric LogQL are only available when Loki is selected")
		}
		value, remaining, err := consumeLogQLQuotedString(rest)
		if err != nil {
			return Keywords{}, err
		}
		rest = remaining
		switch op {
		case "|=":
			exactInclude = append(exactInclude, value)
		case "!=":
			exclude = append(exclude, value)
		case "|~", "!~":
			values, err := literalLogQLRegexValues(value)
			if err != nil {
				return Keywords{}, fmt.Errorf("logquery: line filter %s: %w", op, err)
			}
			if op == "|~" {
				if len(regexInclude) > 0 {
					return Keywords{}, errors.New("logquery: multiple positive regex filters are only available when Loki is selected")
				}
				regexInclude = values
			} else {
				exclude = append(exclude, values...)
			}
		}
	}
	if len(regexInclude) > 0 && len(exactInclude) > 0 {
		return Keywords{}, errors.New("logquery: mixed positive line filters are only available when Loki is selected")
	}
	keywords := Keywords{Exclude: exclude, Mode: MatchAny}
	switch {
	case len(regexInclude) > 0:
		keywords.Include = regexInclude
		keywords.Mode = MatchAny
	case len(exactInclude) == 1:
		keywords.Include = exactInclude
		keywords.Mode = MatchPhrase
	case len(exactInclude) > 1:
		keywords.Include = exactInclude
		keywords.Mode = MatchAll
	}
	return keywords, nil
}

func consumeLogQLQuotedString(input string) (string, string, error) {
	if input == "" || input[0] != '"' {
		return "", "", errors.New("logquery: Elasticsearch query_logql filters require a quoted string")
	}
	escaped := false
	for i := 1; i < len(input); i++ {
		switch input[i] {
		case '\\':
			escaped = !escaped
		case '"':
			if !escaped {
				value, err := strconv.Unquote(input[:i+1])
				if err != nil {
					return "", "", fmt.Errorf("logquery: invalid quoted LogQL string: %w", err)
				}
				return value, input[i+1:], nil
			}
			escaped = false
		default:
			escaped = false
		}
	}
	return "", "", errors.New("logquery: unterminated quoted LogQL string")
}

func literalLogQLRegexValues(pattern string) ([]string, error) {
	pattern = strings.TrimSpace(pattern)
	if strings.HasPrefix(pattern, "(?i)") {
		pattern = strings.TrimSpace(strings.TrimPrefix(pattern, "(?i)"))
	}
	if strings.HasPrefix(pattern, "^") && strings.HasSuffix(pattern, "$") && len(pattern) >= 2 {
		pattern = strings.TrimSpace(pattern[1 : len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "(") && strings.HasSuffix(pattern, ")") && len(pattern) >= 2 {
		pattern = strings.TrimSpace(pattern[1 : len(pattern)-1])
	}
	parts := strings.Split(pattern, "|")
	if len(parts) > MaxKeywordCount {
		return nil, fmt.Errorf("literal alternation has more than %d values", MaxKeywordCount)
	}
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value == "" {
			return nil, errors.New("empty regex alternative is not supported")
		}
		if strings.ContainsAny(value, `.\\[]{}()*+?^$`) {
			return nil, errors.New("only literal regex alternatives are supported on Elasticsearch")
		}
		values = append(values, value)
	}
	return values, nil
}
