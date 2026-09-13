package workerpulse

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	JobModelSync             = "model_sync"
	JobSubscriptionExpiry    = "subscription_expiry"
	JobSubscriptionReminder  = "subscription_reminder"
	JobSubscriptionAutoRenew = "subscription_auto_renew"

	RoleExecutor = "executor"
	RoleFollower = "follower"
	RoleStale    = "stale"

	replicaEnv     = "HUAKAI_REPLICA_ID"
	unknownReplica = "unknown-replica"
)

// ReplicaID 返回本进程副本标识。优先环境变量,否则主机名;都不可用时用固定占位,
// 避免空串被当成全集群。
func ReplicaID() string {
	if value := strings.TrimSpace(os.Getenv(replicaEnv)); value != "" {
		return value
	}
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		name = unknownReplica
	}
	return fmt.Sprintf("%s-%d", strings.TrimSpace(name), os.Getpid())
}

// Freshness 超过该窗口的心跳视为陈旧,不能再冒充当前执行者。
func Freshness(jobKey string) time.Duration {
	switch jobKey {
	case JobModelSync:
		return 7 * time.Hour
	case JobSubscriptionAutoRenew:
		return 12 * time.Minute
	default:
		return 3 * time.Minute
	}
}
