// Package servermonitorhttp 提供部署管理员读取网关实例监测数据的接口。
package servermonitorhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/servermonitor"
)

type Store interface {
	ListNodes(context.Context, time.Time, time.Duration, int, int) ([]servermonitor.Node, error)
	GetNode(context.Context, string, time.Time, time.Duration) (servermonitor.Node, error)
	ListHistory(context.Context, string, time.Time, time.Time, int) ([]servermonitor.HistoryPoint, error)
}

type Handler struct {
	store        Store
	offlineAfter time.Duration
	now          func() time.Time
}

type ListResponse struct {
	Nodes               []servermonitor.Node `json:"nodes"`
	Limit               int                  `json:"limit"`
	Offset              int                  `json:"offset"`
	OfflineAfterSeconds int64                `json:"offline_after_seconds"`
}

type DetailResponse struct {
	Node                servermonitor.Node `json:"node"`
	OfflineAfterSeconds int64              `json:"offline_after_seconds"`
}

type HistoryResponse struct {
	NodeID                  string                       `json:"node_id"`
	From                    time.Time                    `json:"from"`
	To                      time.Time                    `json:"to"`
	Bucket                  string                       `json:"bucket"`
	SourceResolutionSeconds int64                        `json:"source_resolution_seconds"`
	BucketSeconds           int64                        `json:"bucket_seconds"`
	GapSemantics            string                       `json:"gap_semantics"`
	Points                  []servermonitor.HistoryPoint `json:"points"`
}

func New(store Store, offlineAfter time.Duration) *Handler {
	if offlineAfter <= 0 {
		offlineAfter = servermonitor.DefaultOfflineAfter
	}
	return &Handler{store: store, offlineAfter: offlineAfter, now: time.Now}
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_not_configured", "server monitor is not configured")
		return
	}
	limit, ok := parseBoundedInt(r.URL.Query().Get("limit"), 100, 1, 500)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be between 1 and 500")
		return
	}
	offset, ok := parseBoundedInt(r.URL.Query().Get("offset"), 0, 0, 1_000_000)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be between 0 and 1000000")
		return
	}
	nodes, err := h.store.ListNodes(r.Context(), h.now().UTC(), h.offlineAfter, limit, offset)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_unavailable", "server monitor data is temporarily unavailable")
		return
	}
	if nodes == nil {
		nodes = []servermonitor.Node{}
	}
	writeJSON(w, http.StatusOK, ListResponse{
		Nodes:               nodes,
		Limit:               limit,
		Offset:              offset,
		OfflineAfterSeconds: int64(h.offlineAfter.Seconds()),
	})
}

func (h *Handler) Detail(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_not_configured", "server monitor is not configured")
		return
	}
	nodeID := strings.TrimSpace(chi.URLParam(r, "node_id"))
	if !servermonitor.ValidateNodeID(nodeID) {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "node_id is invalid")
		return
	}
	node, err := h.store.GetNode(r.Context(), nodeID, h.now().UTC(), h.offlineAfter)
	if errors.Is(err, servermonitor.ErrNodeNotFound) {
		writeError(w, http.StatusNotFound, "server_monitor_node_not_found", "server monitor node was not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_unavailable", "server monitor data is temporarily unavailable")
		return
	}
	writeJSON(w, http.StatusOK, DetailResponse{Node: node, OfflineAfterSeconds: int64(h.offlineAfter.Seconds())})
}

func (h *Handler) History(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.store == nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_not_configured", "server monitor is not configured")
		return
	}
	nodeID := strings.TrimSpace(chi.URLParam(r, "node_id"))
	if !servermonitor.ValidateNodeID(nodeID) {
		writeError(w, http.StatusBadRequest, "invalid_node_id", "node_id is invalid")
		return
	}
	now := h.now().UTC()
	if _, err := h.store.GetNode(r.Context(), nodeID, now, h.offlineAfter); errors.Is(err, servermonitor.ErrNodeNotFound) {
		writeError(w, http.StatusNotFound, "server_monitor_node_not_found", "server monitor node was not found")
		return
	} else if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_unavailable", "server monitor data is temporarily unavailable")
		return
	}
	to, ok := parseTimeQuery(r.URL.Query().Get("to"), now)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_to", "to must be RFC3339")
		return
	}
	from, ok := parseTimeQuery(r.URL.Query().Get("from"), to.Add(-24*time.Hour))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_from", "from must be RFC3339")
		return
	}
	if !from.Before(to) {
		writeError(w, http.StatusBadRequest, "invalid_time_range", "from must be earlier than to")
		return
	}
	bucket := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("bucket")))
	if bucket == "" {
		bucket = "minute"
	}
	bucketDuration, maxRange := time.Minute, 7*24*time.Hour
	if bucket == "15m" {
		bucketDuration, maxRange = 15*time.Minute, servermonitor.DefaultRetention
	} else if bucket != "minute" {
		writeError(w, http.StatusBadRequest, "invalid_bucket", "bucket must be minute or 15m")
		return
	}
	if to.Sub(from) > maxRange {
		writeError(w, http.StatusBadRequest, "history_range_too_large", "requested history range is too large for the selected bucket")
		return
	}
	points, err := h.store.ListHistory(r.Context(), nodeID, from, to, 50000)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "server_monitor_unavailable", "server monitor data is temporarily unavailable")
		return
	}
	if bucketDuration > time.Minute {
		points = rollupLast(points, bucketDuration)
	}
	if points == nil {
		points = []servermonitor.HistoryPoint{}
	}
	writeJSON(w, http.StatusOK, HistoryResponse{
		NodeID:                  nodeID,
		From:                    from,
		To:                      to,
		Bucket:                  bucket,
		SourceResolutionSeconds: 60,
		BucketSeconds:           int64(bucketDuration.Seconds()),
		GapSemantics:            "missing",
		Points:                  points,
	})
}

func rollupLast(points []servermonitor.HistoryPoint, bucket time.Duration) []servermonitor.HistoryPoint {
	if len(points) == 0 {
		return []servermonitor.HistoryPoint{}
	}
	out := make([]servermonitor.HistoryPoint, 0, len(points))
	for _, point := range points {
		bucketAt := point.BucketAt.UTC().Truncate(bucket)
		point.BucketAt = bucketAt
		if len(out) > 0 && out[len(out)-1].BucketAt.Equal(bucketAt) {
			out[len(out)-1] = point
			continue
		}
		out = append(out, point)
	}
	return out
}

func parseBoundedInt(raw string, fallback, minimum, maximum int) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, false
	}
	return value, true
}

func parseTimeQuery(raw string, fallback time.Time) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback.UTC(), true
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false
	}
	return value.UTC(), true
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
