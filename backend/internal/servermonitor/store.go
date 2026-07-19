package servermonitor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (s *PostgresStore) WriteSnapshot(ctx context.Context, snapshot Snapshot) error {
	if s == nil || s.pool == nil {
		return errors.New("server monitor store requires a postgres pool")
	}
	if err := snapshot.NormalizeAndValidate(); err != nil {
		return fmt.Errorf("validate server monitor snapshot: %w", err)
	}
	metricsJSON, statesJSON, err := marshalSnapshotJSON(snapshot.Metrics, snapshot.MetricStates)
	if err != nil {
		return err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin server monitor snapshot transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var receivedAt time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&receivedAt); err != nil {
		return fmt.Errorf("read server monitor database clock: %w", err)
	}
	receivedAt = receivedAt.UTC()
	if snapshot.CollectedAt.Before(receivedAt.Add(-5*time.Minute)) || snapshot.CollectedAt.After(receivedAt.Add(5*time.Minute)) {
		return ErrSnapshotClockSkew
	}

	var previousSession uuid.UUID
	var previousStartedAt time.Time
	var previousSequence int64
	var previousActivity time.Time
	var previousErrors []string
	rowErr := tx.QueryRow(ctx, `
SELECT session_id, session_started_at, last_sequence, last_activity_at, active_error_classes
FROM server_monitor_nodes
WHERE node_id = $1
FOR UPDATE`, snapshot.Identity.NodeID).Scan(
		&previousSession,
		&previousStartedAt,
		&previousSequence,
		&previousActivity,
		&previousErrors,
	)
	switch {
	case errors.Is(rowErr, pgx.ErrNoRows):
		if err := insertCurrentNode(ctx, tx, snapshot, metricsJSON, statesJSON, receivedAt); err != nil {
			return err
		}
	case rowErr != nil:
		return fmt.Errorf("lock server monitor node: %w", rowErr)
	case previousSession == snapshot.SessionID && (snapshot.Sequence <= previousSequence || !snapshot.CollectedAt.After(previousActivity)):
		return ErrStaleSnapshot
	case previousSession != snapshot.SessionID && (!snapshot.SessionStartedAt.After(previousStartedAt) || snapshot.CollectedAt.Before(previousActivity)):
		return ErrStaleSnapshot
	default:
		if err := updateCurrentNode(ctx, tx, snapshot, metricsJSON, statesJSON, receivedAt, previousErrors); err != nil {
			return err
		}
	}
	if err := upsertHistoryPoint(ctx, tx, snapshot, metricsJSON, statesJSON, receivedAt); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit server monitor snapshot: %w", err)
	}
	return nil
}

func insertCurrentNode(ctx context.Context, tx pgx.Tx, snapshot Snapshot, metricsJSON, statesJSON []byte, receivedAt time.Time) error {
	var lastSuccessAt, lastErrorAt any
	if snapshot.CollectionStatus == CollectionStatusSuccess {
		lastSuccessAt = snapshot.CollectedAt
	}
	if len(snapshot.ActiveErrorClasses) > 0 {
		lastErrorAt = snapshot.CollectedAt
	}
	_, err := tx.Exec(ctx, `
INSERT INTO server_monitor_nodes (
    node_id, display_name, identity_source, identity_stable, source_kind, view_scope,
    session_id, session_started_at, last_sequence, last_activity_at,
    last_success_at, last_error_at, collection_status, active_error_classes,
    os_name, os_arch, metrics, metric_states, created_at, updated_at
) VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10,
    $11, $12, $13, $14,
    $15, $16, $17::jsonb, $18::jsonb, $19, $19
)`,
		snapshot.Identity.NodeID,
		snapshot.Identity.DisplayName,
		snapshot.Identity.Source,
		snapshot.Identity.Stable,
		snapshot.SourceKind,
		snapshot.ViewScope,
		snapshot.SessionID,
		snapshot.SessionStartedAt,
		snapshot.Sequence,
		snapshot.CollectedAt,
		lastSuccessAt,
		lastErrorAt,
		snapshot.CollectionStatus,
		snapshot.ActiveErrorClasses,
		snapshot.OSName,
		snapshot.OSArch,
		metricsJSON,
		statesJSON,
		receivedAt,
	)
	if err != nil {
		return fmt.Errorf("insert server monitor node: %w", err)
	}
	return nil
}

func updateCurrentNode(ctx context.Context, tx pgx.Tx, snapshot Snapshot, metricsJSON, statesJSON []byte, receivedAt time.Time, previousErrors []string) error {
	recovered := removedAnyError(previousErrors, snapshot.ActiveErrorClasses)
	_, err := tx.Exec(ctx, `
UPDATE server_monitor_nodes
SET display_name = $2,
    identity_source = $3,
    identity_stable = $4,
    source_kind = $5,
    view_scope = $6,
    session_id = $7,
    session_started_at = $8,
    last_sequence = $9,
    last_activity_at = $10,
    last_success_at = CASE WHEN $11 THEN $10 ELSE last_success_at END,
    last_error_at = CASE WHEN cardinality($12::text[]) > 0 THEN $10 ELSE last_error_at END,
    last_recovered_at = CASE WHEN $13 THEN $10 ELSE last_recovered_at END,
    collection_status = $14,
    active_error_classes = $12,
    os_name = $15,
    os_arch = $16,
    metrics = $17::jsonb,
    metric_states = $18::jsonb,
    updated_at = $19
WHERE node_id = $1`,
		snapshot.Identity.NodeID,
		snapshot.Identity.DisplayName,
		snapshot.Identity.Source,
		snapshot.Identity.Stable,
		snapshot.SourceKind,
		snapshot.ViewScope,
		snapshot.SessionID,
		snapshot.SessionStartedAt,
		snapshot.Sequence,
		snapshot.CollectedAt,
		snapshot.CollectionStatus == CollectionStatusSuccess,
		snapshot.ActiveErrorClasses,
		recovered,
		snapshot.CollectionStatus,
		snapshot.OSName,
		snapshot.OSArch,
		metricsJSON,
		statesJSON,
		receivedAt,
	)
	if err != nil {
		return fmt.Errorf("update server monitor node: %w", err)
	}
	return nil
}

func upsertHistoryPoint(ctx context.Context, tx pgx.Tx, snapshot Snapshot, metricsJSON, statesJSON []byte, receivedAt time.Time) error {
	bucketAt := snapshot.CollectedAt.UTC().Truncate(time.Minute)
	_, err := tx.Exec(ctx, `
INSERT INTO server_monitor_samples (
    node_id, bucket_at, collected_at, received_at, session_id, session_started_at,
    sequence, view_scope, collection_status, active_error_classes, metrics, metric_states
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11::jsonb, $12::jsonb)
ON CONFLICT (node_id, bucket_at) DO UPDATE
SET collected_at = EXCLUDED.collected_at,
    received_at = EXCLUDED.received_at,
    session_id = EXCLUDED.session_id,
    session_started_at = EXCLUDED.session_started_at,
    sequence = EXCLUDED.sequence,
    view_scope = EXCLUDED.view_scope,
    collection_status = EXCLUDED.collection_status,
    active_error_classes = EXCLUDED.active_error_classes,
    metrics = EXCLUDED.metrics,
    metric_states = EXCLUDED.metric_states
WHERE (server_monitor_samples.collected_at,
       server_monitor_samples.session_started_at,
       server_monitor_samples.sequence)
    < (EXCLUDED.collected_at,
       EXCLUDED.session_started_at,
       EXCLUDED.sequence)`,
		snapshot.Identity.NodeID,
		bucketAt,
		snapshot.CollectedAt,
		receivedAt,
		snapshot.SessionID,
		snapshot.SessionStartedAt,
		snapshot.Sequence,
		snapshot.ViewScope,
		snapshot.CollectionStatus,
		snapshot.ActiveErrorClasses,
		metricsJSON,
		statesJSON,
	)
	if err != nil {
		return fmt.Errorf("upsert server monitor history: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListNodes(ctx context.Context, now time.Time, offlineAfter time.Duration, limit, offset int) ([]Node, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("server monitor store requires a postgres pool")
	}
	limit, offset = normalizePage(limit, offset)
	rows, err := s.pool.Query(ctx, `
SELECT node_id, display_name, identity_source, identity_stable, source_kind, view_scope,
       session_id, session_started_at, last_sequence, last_activity_at,
       last_success_at, last_error_at, last_recovered_at,
       collection_status, active_error_classes, os_name, os_arch, metrics, metric_states
FROM server_monitor_nodes
ORDER BY last_activity_at DESC, node_id
LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("list server monitor nodes: %w", err)
	}
	defer rows.Close()
	out := make([]Node, 0)
	for rows.Next() {
		node, err := scanNode(rows, now, offlineAfter)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate server monitor nodes: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) GetNode(ctx context.Context, nodeID string, now time.Time, offlineAfter time.Duration) (Node, error) {
	if s == nil || s.pool == nil {
		return Node{}, errors.New("server monitor store requires a postgres pool")
	}
	row := s.pool.QueryRow(ctx, `
SELECT node_id, display_name, identity_source, identity_stable, source_kind, view_scope,
       session_id, session_started_at, last_sequence, last_activity_at,
       last_success_at, last_error_at, last_recovered_at,
       collection_status, active_error_classes, os_name, os_arch, metrics, metric_states
FROM server_monitor_nodes
WHERE node_id = $1`, nodeID)
	node, err := scanNode(row, now, offlineAfter)
	if errors.Is(err, pgx.ErrNoRows) {
		return Node{}, ErrNodeNotFound
	}
	return node, err
}

func (s *PostgresStore) ListHistory(ctx context.Context, nodeID string, from, to time.Time, limit int) ([]HistoryPoint, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("server monitor store requires a postgres pool")
	}
	if limit <= 0 || limit > 50000 {
		limit = 50000
	}
	rows, err := s.pool.Query(ctx, `
SELECT bucket_at, collected_at, session_id, sequence, view_scope,
       collection_status, active_error_classes, metrics, metric_states
FROM server_monitor_samples
WHERE node_id = $1 AND bucket_at >= $2 AND bucket_at <= $3
ORDER BY bucket_at ASC
LIMIT $4`, nodeID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("list server monitor history: %w", err)
	}
	defer rows.Close()
	points := make([]HistoryPoint, 0)
	for rows.Next() {
		var point HistoryPoint
		var metricsJSON, statesJSON []byte
		if err := rows.Scan(
			&point.BucketAt,
			&point.CollectedAt,
			&point.SessionID,
			&point.Sequence,
			&point.ViewScope,
			&point.CollectionStatus,
			&point.ActiveErrorClasses,
			&metricsJSON,
			&statesJSON,
		); err != nil {
			return nil, fmt.Errorf("scan server monitor history: %w", err)
		}
		if err := unmarshalSnapshotJSON(metricsJSON, statesJSON, &point.Metrics, &point.MetricStates); err != nil {
			return nil, err
		}
		points = append(points, point)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate server monitor history: %w", err)
	}
	return points, nil
}

func (s *PostgresStore) Summary(ctx context.Context, now time.Time, offlineAfter time.Duration) (Summary, error) {
	if s == nil || s.pool == nil {
		return Summary{}, errors.New("server monitor store requires a postgres pool")
	}
	cutoff := now.UTC().Add(-offlineAfter)
	var summary Summary
	if err := s.pool.QueryRow(ctx, `
SELECT count(*),
       count(*) FILTER (WHERE last_activity_at >= $1),
       count(*) FILTER (WHERE last_activity_at < $1),
       count(*) FILTER (WHERE last_activity_at >= $1 AND collection_status <> 'success')
FROM server_monitor_nodes`, cutoff).Scan(&summary.Total, &summary.Online, &summary.Offline, &summary.Degraded); err != nil {
		return Summary{}, fmt.Errorf("summarize server monitor nodes: %w", err)
	}
	return summary, nil
}

func (s *PostgresStore) Cleanup(ctx context.Context, cutoff time.Time, batch int) (CleanupResult, error) {
	if s == nil || s.pool == nil {
		return CleanupResult{}, errors.New("server monitor store requires a postgres pool")
	}
	if batch <= 0 || batch > 10000 {
		batch = DefaultCleanupBatch
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("begin server monitor cleanup: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	result := CleanupResult{}
	sampleTag, err := tx.Exec(ctx, `
WITH doomed AS (
    SELECT node_id, bucket_at
    FROM server_monitor_samples
    WHERE bucket_at < $1
    ORDER BY bucket_at
    LIMIT $2
)
DELETE FROM server_monitor_samples s
USING doomed d
WHERE s.node_id = d.node_id AND s.bucket_at = d.bucket_at`, cutoff.UTC(), batch)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("delete server monitor history: %w", err)
	}
	result.SamplesDeleted = sampleTag.RowsAffected()
	nodeTag, err := tx.Exec(ctx, `
WITH doomed AS (
    SELECT node_id
    FROM server_monitor_nodes
    WHERE last_activity_at < $1
    ORDER BY last_activity_at
    LIMIT $2
)
DELETE FROM server_monitor_nodes n
USING doomed d
WHERE n.node_id = d.node_id`, cutoff.UTC(), batch)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("delete expired server monitor nodes: %w", err)
	}
	result.NodesDeleted = nodeTag.RowsAffected()
	if err := tx.Commit(ctx); err != nil {
		return CleanupResult{}, fmt.Errorf("commit server monitor cleanup: %w", err)
	}
	return result, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanNode(row rowScanner, now time.Time, offlineAfter time.Duration) (Node, error) {
	var node Node
	var metricsJSON, statesJSON []byte
	if err := row.Scan(
		&node.Identity.NodeID,
		&node.Identity.DisplayName,
		&node.Identity.Source,
		&node.Identity.Stable,
		&node.SourceKind,
		&node.ViewScope,
		&node.SessionID,
		&node.SessionStartedAt,
		&node.LastSequence,
		&node.LastActivityAt,
		&node.LastSuccessAt,
		&node.LastErrorAt,
		&node.LastRecoveredAt,
		&node.CollectionStatus,
		&node.ActiveErrorClasses,
		&node.OSName,
		&node.OSArch,
		&metricsJSON,
		&statesJSON,
	); err != nil {
		return Node{}, err
	}
	if err := unmarshalSnapshotJSON(metricsJSON, statesJSON, &node.Metrics, &node.MetricStates); err != nil {
		return Node{}, err
	}
	node.Online = !node.LastActivityAt.Before(now.UTC().Add(-offlineAfter))
	return node, nil
}

func marshalSnapshotJSON(metrics Metrics, states map[MetricGroup]MetricState) ([]byte, []byte, error) {
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal server monitor metrics: %w", err)
	}
	statesJSON, err := json.Marshal(states)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal server monitor metric states: %w", err)
	}
	return metricsJSON, statesJSON, nil
}

func unmarshalSnapshotJSON(metricsJSON, statesJSON []byte, metrics *Metrics, states *map[MetricGroup]MetricState) error {
	if err := json.Unmarshal(metricsJSON, metrics); err != nil {
		return fmt.Errorf("decode server monitor metrics: %w", err)
	}
	if err := json.Unmarshal(statesJSON, states); err != nil {
		return fmt.Errorf("decode server monitor metric states: %w", err)
	}
	return nil
}

func removedAnyError(previous, current []string) bool {
	currentSet := make(map[string]struct{}, len(current))
	for _, class := range current {
		currentSet[class] = struct{}{}
	}
	for _, class := range previous {
		if _, exists := currentSet[class]; !exists {
			return true
		}
	}
	return false
}

func normalizePage(limit, offset int) (int, int) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
