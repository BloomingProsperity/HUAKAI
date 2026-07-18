package logsink

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/BloomingProsperity/HUAKAI/internal/logcontract"
)

// 运行日志异步入库 sink 使用异常优先双队列、批量落库和超载计数。
// 日志绝不反压业务链路：队列满丢弃、DB 不可用丢弃、panic 隔离，只留观测计数。
// stderr 输出照旧,本 sink 是补充读取面,不替代任何现有日志通道。

// Entry 一条已脱敏的运行日志记录。
type Entry struct {
	Time              time.Time
	Level             string
	Category          string
	EventType         string
	Result            string
	ErrorClass        string
	ErrorCode         string
	Retryable         bool
	Component         string
	Message           string
	ActorKind         string
	ActorRef          string
	TenantID          *int64
	TargetType        string
	TargetRef         string
	RequestID         string
	TraceID           string
	UpstreamRequestID string
	IdempotencyKey    string
	RecoveryState     string
	Attrs             map[string]any
}

// Store 批量落库接口,由 db 层实现。
type Store interface {
	InsertRuntimeLogs(ctx context.Context, entries []Entry) error
}

const (
	defaultPriorityQueueSize = 1024
	defaultInfoQueueSize     = 3072
	defaultBatchSize         = 100
	defaultFlushInterval     = time.Second
)

type Sink struct {
	priorityQueue chan Entry
	infoQueue     chan Entry
	dropped       atomic.Int64
	priorityDrop  atomic.Int64
	infoDrop      atomic.Int64
	inserted      atomic.Int64
	failedBatches atomic.Int64
	lastFlushUnix atomic.Int64
	batchSize     int
	flushInterval time.Duration

	mu      sync.Mutex
	store   Store
	started bool
	done    chan struct{}
}

type Option func(*Sink)

// WithQueueSize 覆盖队列容量(测试用)。
func WithQueueSize(n int) Option {
	return func(s *Sink) {
		if n > 0 {
			priority := n / 4
			if priority < 1 {
				priority = 1
			}
			s.priorityQueue = make(chan Entry, priority)
			s.infoQueue = make(chan Entry, n-priority)
		}
	}
}

// WithBatch 覆盖批量大小与 flush 间隔(测试用)。
func WithBatch(size int, interval time.Duration) Option {
	return func(s *Sink) {
		if size > 0 {
			s.batchSize = size
		}
		if interval > 0 {
			s.flushInterval = interval
		}
	}
}

// New 构造 sink。此时可先接收 Enqueue(进程早期的告警先积压在队列),
// 待 DB 就绪后 Start 挂上 store 开始落库。
func New(opts ...Option) *Sink {
	s := &Sink{
		priorityQueue: make(chan Entry, defaultPriorityQueueSize),
		infoQueue:     make(chan Entry, defaultInfoQueueSize),
		batchSize:     defaultBatchSize,
		flushInterval: defaultFlushInterval,
		done:          make(chan struct{}),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(s)
		}
	}
	return s
}

// Enqueue 非阻塞入队。无分类的普通 Info 不采集；显式分类 Info 与全部 Warn/Error
// 进入独立队列，访问洪峰不能占用异常队列容量。
// 任何调用点都绝不被日志采集拖慢。
func (s *Sink) Enqueue(e Entry) {
	if s == nil {
		return
	}
	var accepted bool
	e, accepted = normalizeEntry(e)
	if !accepted {
		return
	}
	queue := s.priorityQueue
	levelDrop := &s.priorityDrop
	if e.Level == "info" {
		queue = s.infoQueue
		levelDrop = &s.infoDrop
	}
	select {
	case queue <- e:
	default:
		s.dropped.Add(1)
		levelDrop.Add(1)
	}
}

func normalizeEntry(e Entry) (Entry, bool) {
	e.Level = strings.ToLower(strings.TrimSpace(e.Level))
	if e.Level != "info" && e.Level != "warn" && e.Level != "error" {
		return Entry{}, false
	}
	e.Category = strings.TrimSpace(e.Category)
	e.EventType = strings.TrimSpace(e.EventType)
	if e.Level == "info" && e.Category == "" && e.EventType == "" {
		return Entry{}, false
	}
	invalidContract := (e.Category != "" && !logcontract.ValidCategory(e.Category)) ||
		(e.EventType != "" && !logcontract.ValidMachineIdentifier(e.EventType)) ||
		(e.Result != "" && !logcontract.ValidResult(e.Result)) ||
		(e.ErrorClass != "" && !logcontract.ValidErrorClass(e.ErrorClass)) ||
		(e.ErrorCode != "" && !logcontract.ValidMachineIdentifier(e.ErrorCode)) ||
		(e.ActorKind != "" && !logcontract.ValidActorKind(e.ActorKind)) ||
		(e.RecoveryState != "" && !logcontract.ValidRecoveryState(e.RecoveryState))
	if e.Level == "info" && (e.Category == "" || e.EventType == "") {
		invalidContract = true
	}
	if invalidContract {
		e.Level = "error"
		e.Category = string(logcontract.CategoryError)
		e.EventType = "runtime.contract_invalid"
		e.Result = string(logcontract.ResultServerFailure)
		e.ErrorClass = string(logcontract.ErrorDataIntegrity)
		e.ErrorCode = "runtime_log_contract_invalid"
		e.Retryable = false
		e.RecoveryState = string(logcontract.RecoveryOperatorRequired)
	}
	if e.Category == "" {
		e.Category = string(logcontract.CategoryError)
	}
	if e.EventType == "" {
		if e.Level == "error" {
			e.EventType = "runtime.unclassified_error"
		} else {
			e.EventType = "runtime.unclassified_warning"
		}
	}
	if e.Result == "" {
		if e.Level == "info" {
			e.Result = string(logcontract.ResultUnknown)
		} else {
			e.Result = string(logcontract.ResultServerFailure)
		}
	}
	if e.ErrorClass == "" {
		if e.Level == "info" {
			e.ErrorClass = string(logcontract.ErrorNone)
		} else {
			e.ErrorClass = string(logcontract.ErrorUnknown)
		}
	}
	if e.ErrorCode == "" {
		if e.Level == "info" {
			e.ErrorCode = "none"
		} else {
			e.ErrorCode = "runtime_unclassified"
		}
	}
	if !logcontract.ValidActorKind(e.ActorKind) {
		e.ActorKind = string(logcontract.ActorUnknown)
	}
	if e.RecoveryState == "" {
		e.RecoveryState = string(logcontract.RecoveryNone)
	}
	if e.TenantID != nil && *e.TenantID <= 0 {
		e.TenantID = nil
	}
	if e.Attrs == nil {
		e.Attrs = map[string]any{}
	}
	return e, true
}

// Start 挂上 store 并启动落库 worker;重复调用只生效第一次。
// ctx 取消后 worker 尽力 drain 队列再退出。
func (s *Sink) Start(ctx context.Context, store Store) {
	if s == nil || store == nil {
		return
	}
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.store = store
	s.mu.Unlock()
	go s.run(ctx)
}

func (s *Sink) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	batch := make([]Entry, 0, s.batchSize)
	for {
		select {
		case e := <-s.priorityQueue:
			batch = append(batch, e)
			if len(batch) >= s.batchSize {
				s.flush(batch)
				batch = batch[:0]
			}
			continue
		default:
		}
		select {
		case <-ctx.Done():
			s.drain(batch)
			return
		case e := <-s.priorityQueue:
			batch = append(batch, e)
			if len(batch) >= s.batchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case e := <-s.infoQueue:
			batch = append(batch, e)
			if len(batch) >= s.batchSize {
				s.flush(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			s.flush(batch)
			batch = batch[:0]
		}
	}
}

func (s *Sink) drain(batch []Entry) {
	for {
		var e Entry
		var ok bool
		select {
		case e = <-s.priorityQueue:
			ok = true
		default:
			select {
			case e = <-s.infoQueue:
				ok = true
			default:
			}
		}
		if !ok {
			s.flush(batch)
			return
		}
		batch = append(batch, e)
		if len(batch) >= s.batchSize {
			s.flush(batch)
			batch = batch[:0]
		}
	}
}

// flush 批量落库;失败整批计入丢弃(fail-open,不重试不阻塞)。panic 隔离:
// 落库层任何 panic 不许打崩进程,该批按丢弃处理。
func (s *Sink) flush(batch []Entry) {
	if len(batch) == 0 {
		return
	}
	defer func() {
		if recover() != nil {
			s.recordBatchDrop(batch)
			s.failedBatches.Add(1)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.InsertRuntimeLogs(ctx, batch); err != nil {
		s.recordBatchDrop(batch)
		s.failedBatches.Add(1)
		return
	}
	s.inserted.Add(int64(len(batch)))
	s.lastFlushUnix.Store(time.Now().Unix())
}

func (s *Sink) recordBatchDrop(batch []Entry) {
	var info, priority int64
	for _, entry := range batch {
		if entry.Level == "info" {
			info++
		} else {
			priority++
		}
	}
	s.dropped.Add(info + priority)
	s.infoDrop.Add(info)
	s.priorityDrop.Add(priority)
}

// WaitDone 等待落库 worker 退出(drain 完成),超时放弃。从未 Start 过则立即返回。
// 供停机序列在「日志生产者都停了」之后调用:取消 sink ctx → WaitDone → 关 DB。
func (s *Sink) WaitDone(timeout time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	started := s.started
	s.mu.Unlock()
	if !started {
		return
	}
	select {
	case <-s.done:
	case <-time.After(timeout):
	}
}

// Health sink 观测:队列积压 / 已入库 / 已丢弃 / 最后成功落库时刻(0=从未)。
func (s *Sink) Health() (queueLen int, inserted, dropped int64, lastFlush time.Time) {
	if s == nil {
		return 0, 0, 0, time.Time{}
	}
	unix := s.lastFlushUnix.Load()
	var last time.Time
	if unix > 0 {
		last = time.Unix(unix, 0).UTC()
	}
	return len(s.priorityQueue) + len(s.infoQueue), s.inserted.Load(), s.dropped.Load(), last
}

type HealthSnapshot struct {
	QueueLen         int
	QueueCapacity    int
	PriorityQueueLen int
	PriorityCapacity int
	InfoQueueLen     int
	InfoCapacity     int
	Inserted         int64
	Dropped          int64
	PriorityDropped  int64
	InfoDropped      int64
	FailedBatches    int64
	LastFlush        time.Time
}

// DetailedHealth 给运营面返回异常与 Info 队列的独立容量和丢弃量。
func (s *Sink) DetailedHealth() HealthSnapshot {
	if s == nil {
		return HealthSnapshot{}
	}
	_, inserted, dropped, last := s.Health()
	return HealthSnapshot{
		QueueLen:         len(s.priorityQueue) + len(s.infoQueue),
		QueueCapacity:    cap(s.priorityQueue) + cap(s.infoQueue),
		PriorityQueueLen: len(s.priorityQueue),
		PriorityCapacity: cap(s.priorityQueue),
		InfoQueueLen:     len(s.infoQueue),
		InfoCapacity:     cap(s.infoQueue),
		Inserted:         inserted,
		Dropped:          dropped,
		PriorityDropped:  s.priorityDrop.Load(),
		InfoDropped:      s.infoDrop.Load(),
		FailedBatches:    s.failedBatches.Load(),
		LastFlush:        last,
	}
}
