// HUAKAI · iKun

package subscription

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 提醒默认参数。
const (
	// DefaultReminderInterval 提醒扫描周期 (默认 1 小时; 比到期 worker 慢, 提醒非紧急)。
	DefaultReminderInterval = time.Hour
	// DefaultReminderBatchSize 单次扫描批量上限。
	DefaultReminderBatchSize = 300
)

// DefaultReminderOffsetsDays 默认提醒档位 (到期前天数)。可由 worker 配置覆盖。
// band 模型: 每个档位是一个"区间", 剩余时长命中哪个区间就发哪档, 不漏档不重发 (见 currentReminderTier)。
var DefaultReminderOffsetsDays = []int{7, 3, 1}

// 提醒投递结果状态 (与 0074 subscription_expiry_reminders.status CHECK 对齐)。
// sent 现在代表"该档位已被一个副本 claim 并尝试发送"; claim 是跨副本发送闸门。
// 若 claim 后发送失败, 保留该行并记录失败 tick, 采用 at-most-once: 宁可漏一封也不重复轰炸。
const (
	ReminderStatusSent               = "sent"
	ReminderStatusSkippedNoRecipient = "skipped_no_recipient"
)

// ReminderOutcome 是 mailer 发送一封提醒后的结果分类。
type ReminderOutcome int

const (
	// ReminderSent 已投递或已入 DLQ 待重试 (均视为已就该档位处理, 记 sent 去重)。
	ReminderSent ReminderOutcome = iota
	// ReminderSkippedUnconfigured 租户未配 SMTP / 提醒关闭; claim 已落库, 上层记录失败 tick 后不重发同档。
	ReminderSkippedUnconfigured
	// ReminderRetry 发送失败 (配置无效 / 瞬时 / 无 DLQ 吸收); claim 已落库, 上层记录失败 tick。
	// 真瞬时失败若已被 sender 内部 DLQ 吸收会返 nil=Sent, 不会走到这里。
	ReminderRetry
)

// ReminderMailer 发送一封到期提醒邮件; 实现负责 SMTP 设置加载 / 投递 / DLQ。
// 用原始类型而非 email.Message, 使本包核心逻辑与 internal/email 解耦 (适配器在 reminder_mailer.go)。
type ReminderMailer interface {
	SendReminder(ctx context.Context, tenantID int64, to, subject, htmlBody string) ReminderOutcome
}

// ReminderCandidate 是一条待评估提醒的活跃订阅 (store 扫描产出, 附带收件邮箱与套餐名)。
type ReminderCandidate struct {
	TenantID       int64
	SubscriptionID int64
	UserID         int64
	ExpiresAt      time.Time
	RecipientEmail string // 用户邮箱; 空串表示无收件人
	PlanName       string
}

// reminderRecord 写入提醒投递账本的一行。
type reminderRecord struct {
	TenantID       int64
	SubscriptionID int64
	ReminderKey    string
	Status         string
	Recipient      string
	ExpiresAt      time.Time
}

// ReminderCursor 是 ListDueReminder 的翻页游标 (按 (expires_at, id) 升序)。
// 零值游标表示从头开始。翻页按"已取行"推进, 与发送结果无关 —— 否则已记录/已跳过的最早
// 一页会反复占住 LIMIT, 后面的订阅永远扫不到 (饿死)。
type ReminderCursor struct {
	ExpiresAt time.Time
	ID        int64
}

// ReminderStore 是提醒服务需要的存储能力子集 (Store 已实现)。
type ReminderStore interface {
	// ListDueReminder: active 且 expires_at 在 (now, now+within] 且 (expires_at, id) > 游标 的订阅,
	// 按 (expires_at, id) 升序限量返回, 附收件邮箱与套餐名。游标用于在一次 tick 内翻完整个窗口。
	ListDueReminder(ctx context.Context, now time.Time, within time.Duration, after ReminderCursor, limit int) ([]ReminderCandidate, error)
	// SentReminderKeys: 某订阅已记录的档位集合 (任意 status), 用于跳过已处理档位。
	SentReminderKeys(ctx context.Context, tenantID, subscriptionID int64) (map[string]struct{}, error)
	// RecordReminder: 记一条提醒结果 (ON CONFLICT DO NOTHING 幂等), 返回是否新插入。
	RecordReminder(ctx context.Context, rec reminderRecord) (bool, error)
}

// ReminderService 编排到期提醒: 扫描 → 分档 → 去重 → claim → 发送。
type ReminderService struct {
	store       ReminderStore
	mailer      ReminderMailer
	now         func() time.Time
	offsetsDays []int
	window      time.Duration
}

// ReminderOption 配置 ReminderService。
type ReminderOption func(*ReminderService)

// WithReminderClock 注入时钟 (测试用)。
func WithReminderClock(clock func() time.Time) ReminderOption {
	return func(r *ReminderService) {
		if clock != nil {
			r.now = clock
		}
	}
}

// WithReminderOffsets 覆盖提醒档位 (到期前天数); 非法值忽略回默认。
func WithReminderOffsets(offsetsDays []int) ReminderOption {
	return func(r *ReminderService) {
		clean := sanitizeOffsets(offsetsDays)
		if len(clean) > 0 {
			r.offsetsDays = clean
			r.window = maxOffsetWindow(clean)
		}
	}
}

// NewReminderService 构造提醒服务。
func NewReminderService(store ReminderStore, mailer ReminderMailer, opts ...ReminderOption) *ReminderService {
	r := &ReminderService{
		store:       store,
		mailer:      mailer,
		now:         func() time.Time { return time.Now().UTC() },
		offsetsDays: append([]int(nil), DefaultReminderOffsetsDays...),
		window:      maxOffsetWindow(DefaultReminderOffsetsDays),
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// ProcessDueReminders 扫整个提醒窗口 (一次 tick 翻完), 对每条订阅发其当前档位的提醒 (去重)。
// 返回实际发送条数。单条失败不阻断其余 (记 lastErr 返回)。
// 翻页按"已取行" (游标) 推进, 不以发送数当进度 —— 否则已记录/已跳过的最早一页会反复占住
// LIMIT, 窗口里更靠后的订阅永远扫不到 (饿死)。
//
// 去重边界 = RecordReminder 唯一索引先 claim, claim 成功的副本才允许发邮件。
// claim 后发送失败不回滚, 采用 at-most-once: 这是非 money 提醒, 用户体验上避免重复轰炸优先。
func (r *ReminderService) ProcessDueReminders(ctx context.Context, limit int) (int, error) {
	if r == nil || r.store == nil || r.mailer == nil {
		return 0, ErrStoreNotConfigured
	}
	if limit <= 0 || limit > 1000 {
		limit = DefaultReminderBatchSize
	}
	now := r.now()
	sent := 0
	var lastErr error
	var cursor ReminderCursor
	for {
		cands, err := r.store.ListDueReminder(ctx, now, r.window, cursor, limit)
		if err != nil {
			return sent, err
		}
		if len(cands) == 0 {
			break
		}
		for _, c := range cands {
			n, err := r.processCandidate(ctx, now, c)
			if err != nil {
				lastErr = err
			}
			sent += n
		}
		// 游标推进到本页最后一行 (按取行进度, 与发送结果无关)。
		last := cands[len(cands)-1]
		cursor = ReminderCursor{ExpiresAt: last.ExpiresAt, ID: last.SubscriptionID}
		if len(cands) < limit {
			break
		}
	}
	return sent, lastErr
}

// processCandidate 评估单条订阅: 算当前档位 → 去重 → claim → 发送。返回是否实际发送 (0/1)。
func (r *ReminderService) processCandidate(ctx context.Context, now time.Time, c ReminderCandidate) (int, error) {
	tier, ok := currentReminderTier(c.ExpiresAt.Sub(now), r.offsetsDays)
	if !ok {
		return 0, nil
	}
	key := strconv.Itoa(tier)

	// 已记录该档 → 跳过 (正常路径去重)。
	keys, err := r.store.SentReminderKeys(ctx, c.TenantID, c.SubscriptionID)
	if err != nil {
		return 0, err
	}
	if _, done := keys[key]; done {
		return 0, nil
	}

	// 无收件邮箱 → 记 skipped_no_recipient (去重, 不发不进 DLQ)。
	if strings.TrimSpace(c.RecipientEmail) == "" {
		_, err := r.store.RecordReminder(ctx, reminderRecord{
			TenantID:       c.TenantID,
			SubscriptionID: c.SubscriptionID,
			ReminderKey:    key,
			Status:         ReminderStatusSkippedNoRecipient,
			ExpiresAt:      c.ExpiresAt,
		})
		return 0, err
	}

	inserted, err := r.store.RecordReminder(ctx, reminderRecord{
		TenantID:       c.TenantID,
		SubscriptionID: c.SubscriptionID,
		ReminderKey:    key,
		Status:         ReminderStatusSent,
		Recipient:      c.RecipientEmail,
		ExpiresAt:      c.ExpiresAt,
	})
	if err != nil {
		return 0, err
	}
	if !inserted {
		return 0, nil
	}

	subject, body := renderReminderEmail(c.PlanName, c.ExpiresAt, tier)
	outcome := r.mailer.SendReminder(ctx, c.TenantID, c.RecipientEmail, subject, body)
	if outcome == ReminderSent {
		return 1, nil
	}
	outcomeName := reminderOutcomeName(outcome)
	slog.WarnContext(ctx, "subscription reminder send failed after durable claim",
		"tenant_id", c.TenantID,
		"user_subscription_id", c.SubscriptionID,
		"reminder_key", key,
		"outcome", outcomeName)
	return 0, fmt.Errorf("subscription: reminder send failed after durable claim: %s", outcomeName)
}

func reminderOutcomeName(outcome ReminderOutcome) string {
	switch outcome {
	case ReminderSent:
		return "sent"
	case ReminderSkippedUnconfigured:
		return "skipped_unconfigured"
	case ReminderRetry:
		return "retry"
	default:
		return "unknown"
	}
}

// currentReminderTier 返回剩余时长命中的提醒档位 (到期前天数) 及是否有适用档。
// band 模型: 档位升序排列, 命中"满足 remaining <= offset 的最小 offset"那一档。
//
//	例: offsets=[1,3,7], remaining=2d → 命中 3 (48h<=72h, 而 48h>24h); remaining=5d → 7; remaining=8d → 无 (太早)。
//
// 这样每个档位区间互不重叠, 临近到期时只发一档, worker 滞后赶上也只发当前档不补发更早档 (不刷屏)。
// remaining<=0 (已到期) 归到期 worker, 不发提醒。
func currentReminderTier(remaining time.Duration, offsetsDays []int) (int, bool) {
	if remaining <= 0 {
		return 0, false
	}
	sorted := sanitizeOffsets(offsetsDays)
	for _, off := range sorted { // 升序
		if remaining <= time.Duration(off)*24*time.Hour {
			return off, true
		}
	}
	return 0, false // 超过最大档 → 太早
}

// sanitizeOffsets 去掉非正值并升序去重。
func sanitizeOffsets(offsetsDays []int) []int {
	seen := make(map[int]struct{}, len(offsetsDays))
	var out []int
	for _, d := range offsetsDays {
		if d <= 0 {
			continue
		}
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	sort.Ints(out)
	return out
}

// maxOffsetWindow 返回扫描窗口 = 最大档位天数 (无有效档位回退 7 天)。
func maxOffsetWindow(offsetsDays []int) time.Duration {
	max := 0
	for _, d := range offsetsDays {
		if d > max {
			max = d
		}
	}
	if max <= 0 {
		max = 7
	}
	return time.Duration(max) * 24 * time.Hour
}

// renderReminderEmail 生成提醒邮件主题与 HTML 正文 (中文; 套餐名 HTML 转义防注入)。
func renderReminderEmail(planName string, expiresAt time.Time, daysLeft int) (subject, htmlBody string) {
	subject = "HUAKAI 订阅到期提醒"
	plan := strings.TrimSpace(planName)
	if plan == "" {
		plan = "您的订阅"
	}
	plan = html.EscapeString(plan)
	date := expiresAt.UTC().Format("2006-01-02 15:04 UTC")
	htmlBody = fmt.Sprintf(
		`<html><body><p>%s 将于 %s 到期（约剩 %d 天）。</p>`+
			`<p>如需继续使用，请及时续订。</p>`+
			`<p>—— HUAKAI</p></body></html>`,
		plan, date, daysLeft,
	)
	return subject, htmlBody
}
