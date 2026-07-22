package hermesrecovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BloomingProsperity/HUAKAI/internal/dlq"
	"github.com/BloomingProsperity/HUAKAI/internal/hermesops"
	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
	"github.com/BloomingProsperity/HUAKAI/internal/privacy"
)

type ReplayFunc func(context.Context, int64, string) (*dlq.Record, error)

type Worker struct {
	store      *Store
	replay     ReplayFunc
	workerID   string
	period     time.Duration
	leaseTTL   time.Duration
	opTimeout  time.Duration
	retryAfter time.Duration

	startOnce sync.Once
	done      chan struct{}
}

func NewWorker(store *Store, replay ReplayFunc) (*Worker, error) {
	if store == nil || store.pool == nil || replay == nil {
		return nil, fmt.Errorf("hermes 变更恢复任务依赖未接线")
	}
	return &Worker{
		store:      store,
		replay:     replay,
		workerID:   "hermes-recovery-" + uuid.NewString(),
		period:     10 * time.Second,
		leaseTTL:   2 * time.Minute,
		opTimeout:  90 * time.Second,
		retryAfter: 30 * time.Second,
		done:       make(chan struct{}),
	}, nil
}

func (w *Worker) Start(ctx context.Context) {
	if w == nil {
		return
	}
	w.startOnce.Do(func() {
		go w.loop(ctx)
	})
}

func (w *Worker) Wait(ctx context.Context) error {
	if w == nil {
		return nil
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.done)
	for {
		processed, err := w.RunOnce(ctx)
		if err != nil {
			_ = privacy.LogSystem(ctx, privacy.SystemEvent{
				Severity:   privacy.SeverityError,
				Component:  "hermes.mutation_recovery",
				ErrorClass: privacy.ErrorClassFor(ctx, err),
				Attrs: map[string]any{
					"event_type": "hermes.mutation_recovery.failed",
				},
			})
		}
		if processed {
			continue
		}
		timer := time.NewTimer(w.period)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if w == nil || w.store == nil || w.replay == nil {
		return false, fmt.Errorf("hermes 变更恢复任务未配置")
	}
	entry, err := w.store.Claim(ctx, w.workerID, w.leaseTTL)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	if entry.ResultStatus == "prepared" {
		if err := w.replayPrepared(ctx, entry); err != nil {
			w.release(entry.OperationID)
			return true, err
		}
	}
	if err := w.store.FinalizeAudit(ctx, entry.OperationID); err != nil {
		w.release(entry.OperationID)
		return true, err
	}
	slog.Info("Hermes 变更恢复完成",
		logcontract.FieldCategory, string(logcontract.CategoryRecovery),
		logcontract.FieldEventType, "hermes.mutation_recovery.completed",
		logcontract.FieldResult, string(logcontract.ResultSuccess),
		"operation_id", entry.OperationID.String(),
		"tool_name", entry.ToolName,
		"target_id", entry.TargetID,
	)
	return true, nil
}

func (w *Worker) replayPrepared(parent context.Context, entry Entry) error {
	ctx, cancel := context.WithTimeout(parent, w.opTimeout)
	defer cancel()
	actorID := entry.ActorSource + ":" + strconv.FormatInt(entry.ActorID, 10)
	record, replayErr := w.replay(ctx, entry.TargetID, actorID)
	status := hermesops.ResultOK
	errorClass := ""
	summary := map[string]any{
		"dlq_id":     entry.TargetID,
		"recovered":  true,
		"idempotent": false,
	}
	if errors.Is(replayErr, dlq.ErrNotFound) {
		replayErr = nil
		summary["status"] = "already_processed"
		summary["idempotent"] = true
	} else if replayErr != nil {
		if !isTerminalReplayError(replayErr) {
			return replayErr
		}
		status = hermesops.ResultError
		errorClass = "mutation_failed"
	} else if record != nil {
		summary["status"] = string(record.Status)
		summary["replay_attempts"] = record.ReplayAttempts
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if err := w.store.RecordClaimedOutcome(finishCtx, entry, w.workerID, status, summary, errorClass, time.Now().UTC()); err != nil {
		return errors.Join(replayErr, err)
	}
	return nil
}

func isTerminalReplayError(err error) bool {
	return errors.Is(err, dlq.ErrUnretryable) || errors.Is(err, dlq.ErrNoHandler)
}

func (w *Worker) release(operationID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = w.store.Release(ctx, operationID, w.workerID, w.retryAfter)
}
