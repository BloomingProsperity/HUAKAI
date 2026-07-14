package poolaccountadmin

import (
	"net/http"
	"strings"
)

// ListOptions 是 provider account 列表路由的已校验查询条件。
type ListOptions struct {
	Limit       int32
	AfterID     int64
	Cursor      *string
	PoolGroupID int64
	StateFilter string
	TagFilter   string
}

// ParseListOptions 一次解析列表路由的全部查询参数。
func ParseListOptions(r *http.Request) (ListOptions, *RequestError) {
	limit, err := ParseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return ListOptions{}, &RequestError{http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 200"}
	}
	afterID, cursor, err := ParseCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		return ListOptions{}, &RequestError{http.StatusBadRequest, "invalid_cursor", "cursor must be an opaque base64 cursor"}
	}
	poolGroupID := int64(0)
	if raw := strings.TrimSpace(r.URL.Query().Get("pool_group_id")); raw != "" {
		poolGroupID, err = ParsePositiveID(raw)
		if err != nil {
			return ListOptions{}, &RequestError{http.StatusBadRequest, "invalid_pool_group_id", "pool_group_id must be a positive int64"}
		}
	}
	stateFilter, err := ParseStateFilter(r.URL.Query().Get("state_filter"))
	if err != nil {
		return ListOptions{}, &RequestError{http.StatusBadRequest, "invalid_state_filter", "state_filter is invalid"}
	}
	return ListOptions{
		Limit: limit, AfterID: afterID, Cursor: cursor, PoolGroupID: poolGroupID,
		StateFilter: stateFilter, TagFilter: strings.TrimSpace(r.URL.Query().Get("tag")),
	}, nil
}
