package logquery

// QueryLogQLResult is the closed result set returned by query_logql. The
// concrete value always matches the single selected backend: Loki returns its
// native QueryRangeResult, while Elasticsearch returns SearchResult.
type QueryLogQLResult interface {
	isQueryLogQLResult()
}

func (*QueryRangeResult) isQueryLogQLResult() {}
func (*SearchResult) isQueryLogQLResult()     {}
