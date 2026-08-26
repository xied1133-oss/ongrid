package logs

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ongridio/ongrid/internal/pkg/logquery"
)

type lokiRangeQuerier interface {
	QueryRange(context.Context, logquery.QueryRangeOptions) (*logquery.QueryRangeResult, error)
}

// QueryLogQL routes the stable query_logql input through the currently selected
// backend. Loki receives the original expression and returns QueryRangeResult;
// Elasticsearch receives the safe log-search subset and returns SearchResult.
func (s *Service) QueryLogQL(ctx context.Context, opts logquery.QueryRangeOptions) (logquery.QueryLogQLResult, error) {
	backend, err := s.selectedBackend(ctx)
	if err != nil {
		return nil, err
	}
	if backend == nil {
		querier, ok := s.loki.(lokiRangeQuerier)
		if !ok || querier == nil {
			return nil, errors.New("current Loki backend does not support LogQL range queries")
		}
		return querier.QueryRange(ctx, opts)
	}

	req, err := logquery.CompileLogQLSearch(opts)
	if err != nil {
		return nil, err
	}
	searcher, err := s.elasticsearchClient(ctx, backend)
	if err != nil {
		return nil, err
	}
	desired := opts.Limit
	if desired <= 0 {
		desired = logquery.MaxSearchLimit
	}
	combined, err := searchLogQLPages(ctx, searcher, req, desired)
	if err != nil {
		return nil, err
	}
	return combined, nil
}

func searchLogQLPages(ctx context.Context, searcher logquery.Searcher, req logquery.SearchRequest, desired int) (_ *logquery.SearchResult, retErr error) {
	started := time.Now()
	openCursor := req.Cursor
	defer func() {
		if err := logquery.CloseCursor(context.WithoutCancel(ctx), searcher, openCursor); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close Elasticsearch search cursor: %w", err))
		}
	}()
	combined := &logquery.SearchResult{
		Records:  make([]logquery.Record, 0, desired),
		Backends: []string{},
	}
	for len(combined.Records) < desired {
		req.Limit = min(logquery.MaxSearchLimit, desired-len(combined.Records))
		page, err := searcher.Search(ctx, req)
		if err != nil {
			return nil, err
		}
		if page == nil {
			return nil, errors.New("logquery: Elasticsearch returned an empty search page")
		}
		openCursor = page.NextCursor
		if len(page.Records) > req.Limit {
			return nil, errors.New("logquery: Elasticsearch returned more records than requested")
		}
		combined.Records = append(combined.Records, page.Records...)
		if len(combined.Backends) == 0 {
			combined.Backends = append(combined.Backends, page.Backends...)
		}
		if !page.HasMore {
			break
		}
		if len(page.Records) == 0 {
			return nil, errors.New("logquery: Elasticsearch page reports more records without returning progress")
		}
		if page.NextCursor == "" {
			return nil, fmt.Errorf("logquery: Elasticsearch page reports more records without a cursor")
		}
		if len(combined.Records) >= desired {
			combined.HasMore = true
			break
		}
		req.Cursor = page.NextCursor
	}
	combined.TookMS = time.Since(started).Milliseconds()
	return combined, nil
}
