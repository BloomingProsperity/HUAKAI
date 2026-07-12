package logsink

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// 运行日志异步入库 sink:只收 warn 及以上,有界队列 + 批量落库 + 超载丢弃计数。
// 日志绝不反压业务链路——队列满丢弃、DB 不可用丢弃、panic 隔离,只留观测计数。
// stderr 输出照旧,本 sink 是补充读取面,不替代任何现有日志通道。

// Entry 一条已脱敏的运行日志记录。
type Entry struct {
	Time      time.Time
	Level     string // "warn" | "error"
	Component string
	Message   string
	RequestID string
	Attrs     map[string]any
}

// Store 批量落库接口,由 db 层实现。
type Store interface {
	InsertRuntimeLogs(ctx context.Context, entries []Entry) error
}

const (
	defaultQueueSize     = 4096
	defaultBatchSize     = 100
	defaultFlushInterval = time.Second
)

type Sink struct {
	queue         chan Entry
	dropped       atomic.Int64
	inserted      atomic.Int64
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
			s.queue = make(chan Entry, n)
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
		queue:         make(chan Entry, defaultQueueSize),
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

// Enqueue 非阻塞入队;队列满或级别不符直接丢弃(丢弃计数可观测)。
// 任何调用点都绝不被日志采集拖慢。
func (s *Sink) Enqueue(e Entry) {
	if s == nil {
		return
	}
	if e.Level != "warn" && e.Level != "error" {
		return
	}
	select {
	case s.queue <- e:
	default:
		s.dropped.Add(1)
	}
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
		case <-ctx.Done():
			// 停机 drain:把队列里已有的都刷完(不再等新条目)。
			for {
				select {
				case e := <-s.queue:
					batch = append(batch, e)
					if len(batch) >= s.batchSize {
						s.flush(batch)
						batch = batch[:0]
					}
				default:
					s.flush(batch)
					return
				}
			}
		case e := <-s.queue:
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

// flush 批量落库;失败整批计入丢弃(fail-open,不重试不阻塞)。panic 隔离:
// 落库层任何 panic 不许打崩进程,该批按丢弃处理。
func (s *Sink) flush(batch []Entry) {
	if len(batch) == 0 {
		return
	}
	defer func() {
		if recover() != nil {
			s.dropped.Add(int64(len(batch)))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.InsertRuntimeLogs(ctx, batch); err != nil {
		s.dropped.Add(int64(len(batch)))
		return
	}
	s.inserted.Add(int64(len(batch)))
	s.lastFlushUnix.Store(time.Now().Unix())
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
	return len(s.queue), s.inserted.Load(), s.dropped.Load(), last
}
