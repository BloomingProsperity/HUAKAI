package subscription

import (
	"context"
	"log/slog"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/workerpulse"
)

// LeaderLease 保证多副本每轮只有一个执行者。存储失败必须跳过本拍,不得全员开跑。
type LeaderLease interface {
	TryAcquire(context.Context) (bool, func(), error)
}

func runGuardedTick(ctx context.Context, lease LeaderLease, pulse workerpulse.Recorder, replicaID, jobKey string, work func() error) (executed bool, workErr error) {
	if replicaID == "" {
		replicaID = workerpulse.ReplicaID()
	}
	if lease != nil {
		acquired, release, err := lease.TryAcquire(ctx)
		if err != nil {
			recordPulse(ctx, pulse, replicaID, jobKey, false, err)
			return false, err
		}
		if !acquired {
			recordPulse(ctx, pulse, replicaID, jobKey, false, nil)
			return false, nil
		}
		if release != nil {
			defer release()
		}
	}
	workErr = work()
	recordPulse(ctx, pulse, replicaID, jobKey, true, workErr)
	return true, workErr
}

func recordPulse(ctx context.Context, pulse workerpulse.Recorder, replicaID, jobKey string, executor bool, workErr error) {
	if pulse == nil {
		return
	}
	now := time.Now().UTC()
	rec := workerpulse.Record{
		JobKey:    jobKey,
		ReplicaID: replicaID,
		SeenAt:    now,
		Executor:  executor,
	}
	if workErr != nil {
		rec.LastError = workErr.Error()
	} else if executor {
		rec.SuccessAt = now
	}
	if err := pulse.Record(ctx, rec); err != nil {
		slog.WarnContext(ctx, "后台作业心跳写入失败",
			"component", "subscription_worker",
			"job", jobKey,
			"replica", replicaID,
			"error", err.Error())
	}
}
