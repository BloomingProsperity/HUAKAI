//go:build integration_pg

package accountcreate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/db"
	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/mixedchannelrisk"
)

// TestInsertBlocksOnAdvisoryLockHeldExternally 咬住 §17 并发关键路径,且是**确定性**
// 判别(不依赖 goroutine 调度巧合):在测试内用一条独立连接、按生产同款 key 抢占
// 同一 tenant/channel 的 pg_advisory_xact_lock 并保持事务不提交;此时对同一 channel
// 调用生产 Insert,它进入自身事务后必然阻塞在同款 advisory lock 上,拿不到锁 →
// 无法读 peers/评估/插入。释放外部锁后 Insert 才推进完成。
//
// 判别契约(有锁,确定性):外部持锁期间 Insert 不返回;释放后返回。
// 变异:删掉 atomic.go 的 pg_advisory_xact_lock 行 → Insert 不再抢该锁 → 外部持锁
// 期间就直接跑完返回 → “持锁期间不返回”断言变红。这是确定性红,不靠竞态。
func TestInsertBlocksOnAdvisoryLockHeldExternally(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openAccountCreatePool(t, ctx)
	seed := seedTwoProviderChannel(t, ctx, pool)

	// 生产 key:见 atomic.go —— "provider-account-mixed-risk:<tenant>:<channel>"。
	lockKey := fmt.Sprintf("provider-account-mixed-risk:%d:%d", seed.tenantID, seed.channelID)

	// 独立连接开事务并抢占同款 advisory lock,事务不提交 → 锁一直被外部持有。
	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取持锁连接: %v", err)
	}
	defer holder.Release()
	holdTx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("持锁事务 Begin: %v", err)
	}
	if _, err := holdTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		t.Fatalf("外部抢占 advisory lock: %v", err)
	}

	params := Params{
		Insert: admindb.InsertProviderAccountParams{
			TenantID: seed.tenantID, ProviderID: seed.providerA, ChannelID: seed.channelID,
			Name: "blocked-" + seed.suffix, AccountType: "api_key",
			Credentials: []byte("{}"), Extra: []byte("{}"),
		},
		Candidate: mixedchannelrisk.Account{
			ProviderID: seed.providerA, ChannelID: seed.channelID,
			AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
		},
		ProviderFamily: "openai_chat",
		Confirmed:      false,
	}

	done := make(chan error, 1)
	go func() {
		_, err := Insert(ctx, pool, params)
		done <- err
	}()

	// 外部持锁期间 Insert 必须阻塞:400ms 内不得返回。
	select {
	case err := <-done:
		t.Fatalf("外部持有 advisory lock 时 Insert 却返回(err=%v):说明 Insert 未抢该锁(删锁变异态)", err)
	case <-time.After(400 * time.Millisecond):
		// 期望:仍被阻塞。
	}

	// 释放外部锁,Insert 应随即推进并完成。
	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("释放持锁事务: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("释放锁后 Insert 应成功(空 channel 无风险),却 err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("释放 advisory lock 后 Insert 仍未完成")
	}
}

// TestInsertLockPrecedesPeersRead 咬住**锁序不变量**:advisory lock 必须在读 peers
// 之前获取,否则两个并发创建会各自先读到空 peers 再抢锁,绕过混合风险门。
// 上面的阻塞测试只证"Insert 会等锁",证不了"锁在读 peers 之前";本测试补上。
//
// 构造:外部先持锁 → 启动 Insert(A) → 等一小段(正确态 A 被锁死在读 peers 前;若
// 锁被挪到读 peers 之后,A 会先读到空 peers)→ 直插一个不同 provider 的冲突对端并
// 提交 → 释放锁 → A 推进。
// 判别:正确态 A 拿锁后才读 peers,读到已提交的冲突对端 → HighRisk → 拒绝(绿)。
// 变异(把 atomic.go 的 lock 挪到 ListProviderAccountRiskPeers 之后):A 早读到空
// peers → 无风险 → 插入成功,本测试的"应拒绝"断言红。确定性抓锁序。
func TestInsertLockPrecedesPeersRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openAccountCreatePool(t, ctx)
	seed := seedTwoProviderChannel(t, ctx, pool)
	lockKey := fmt.Sprintf("provider-account-mixed-risk:%d:%d", seed.tenantID, seed.channelID)

	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取持锁连接: %v", err)
	}
	defer holder.Release()
	holdTx, err := holder.Begin(ctx)
	if err != nil {
		t.Fatalf("持锁事务 Begin: %v", err)
	}
	if _, err := holdTx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1::text, 0))`, lockKey); err != nil {
		t.Fatalf("外部抢锁: %v", err)
	}

	insertA := Params{
		Insert: admindb.InsertProviderAccountParams{
			TenantID: seed.tenantID, ProviderID: seed.providerA, ChannelID: seed.channelID,
			Name: "order-A-" + seed.suffix, AccountType: "api_key",
			Credentials: []byte("{}"), Extra: []byte("{}"),
		},
		Candidate: mixedchannelrisk.Account{
			ProviderID: seed.providerA, ChannelID: seed.channelID,
			AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
		},
		ProviderFamily: "openai_chat",
		Confirmed:      false,
	}
	done := make(chan error, 1)
	go func() {
		_, err := Insert(ctx, pool, insertA)
		done <- err
	}()

	// 确定性 barrier(替代猜测性 sleep):轮询 pg_locks 直到观测到 A 的后端真的**阻塞在
	// advisory lock 上**(有一个未授予的 advisory 锁——外部持锁方那把是已授予的)。
	// 正确态:A 阻塞在锁上时**尚未读 peers**(锁在读之前)→ 下面插入的冲突对端会被 A 读到 → 拒。
	// 变异态(锁挪到读 peers 之后):A 阻塞在锁上时**已读完空 peers**(读在锁之前,冲突对端此刻还没插)
	//   → 释放后 A 用陈旧空 peers 评估 → 成功 → 本测试"应拒绝"断言红。两态在此 barrier 下都确定。
	waitForBlockedAdvisoryLock(t, ctx, pool)

	// 直插一个不同 provider 的冲突对端(同 channel),已提交。
	var peerID int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO provider_accounts (tenant_id, provider_id, channel_id, name, account_type, credentials, extra)
		 VALUES ($1,$2,$3,$4,'api_key','{}','{}') RETURNING id`,
		seed.tenantID, seed.providerB, seed.channelID, "order-peer-"+seed.suffix,
	).Scan(&peerID); err != nil {
		t.Fatalf("直插冲突对端: %v", err)
	}

	if err := holdTx.Rollback(ctx); err != nil {
		t.Fatalf("释放锁: %v", err)
	}

	select {
	case err := <-done:
		if !isMixedRiskConfirmRequired(err) {
			t.Fatalf("锁序正确时 A 应在拿锁后读到冲突对端并被高危门拒,却 err=%v(锁被挪到 peers 读之后?)", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("释放锁后 Insert(A) 仍未完成")
	}
}

// TestInsertBlocksAndRejectsWhenProtocolChangesConcurrently 咬住 S1-5:创建事务读
// provider 协议必须用 FOR SHARE(与管理端改 upstream_protocol 的 FOR NO KEY UPDATE
// 冲突),否则并发协议变更下会读到旧快照并插入不兼容账号(TOCTOU)。
//
// 判别契约(FOR SHARE):外部持有 upstream_protocol 更新事务期间,Insert 的协议读
// 阻塞;提交后 Insert 读到新协议 → family 与请求不符 → ErrProtocolIncompatible。
// 变异:把查询改回 FOR KEY SHARE → 不与非键更新冲突 → Insert 读旧快照 openai_chat
// → family 匹配 → 插入成功(err=nil),本测试两处断言均红。确定性,不靠竞态。
func TestInsertBlocksAndRejectsWhenProtocolChangesConcurrently(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool := openAccountCreatePool(t, ctx)
	seed := seedTwoProviderChannel(t, ctx, pool)

	// 外部事务改 providerA 的 upstream_protocol(非键列 → FOR NO KEY UPDATE),不提交。
	updater, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("获取更新连接: %v", err)
	}
	defer updater.Release()
	updTx, err := updater.Begin(ctx)
	if err != nil {
		t.Fatalf("更新事务 Begin: %v", err)
	}
	if _, err := updTx.Exec(ctx,
		`UPDATE providers SET upstream_protocol='anthropic_messages' WHERE tenant_id=$1 AND id=$2`,
		seed.tenantID, seed.providerA,
	); err != nil {
		t.Fatalf("外部改协议: %v", err)
	}

	params := Params{
		Insert: admindb.InsertProviderAccountParams{
			TenantID: seed.tenantID, ProviderID: seed.providerA, ChannelID: seed.channelID,
			Name: "proto-race-" + seed.suffix, AccountType: "api_key",
			Credentials: []byte("{}"), Extra: []byte("{}"),
		},
		Candidate: mixedchannelrisk.Account{
			ProviderID: seed.providerA, ChannelID: seed.channelID,
			AccountType: "api_key", Vendor: "openai", AuthMode: "api_key",
		},
		ProviderFamily: "openai_chat", // 请求方按旧协议构造;并发更新会使其失效
		Confirmed:      false,
	}

	done := make(chan error, 1)
	go func() {
		_, err := Insert(ctx, pool, params)
		done <- err
	}()

	// 外部更新未提交时,Insert 的 FOR SHARE 协议读必须阻塞。
	select {
	case err := <-done:
		t.Fatalf("协议更新事务未提交时 Insert 却返回(err=%v):FOR SHARE 未锁住协议(FOR KEY SHARE 变异态)", err)
	case <-time.After(400 * time.Millisecond):
	}

	if err := updTx.Commit(ctx); err != nil {
		t.Fatalf("提交协议更新: %v", err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrProtocolIncompatible) {
			t.Fatalf("协议变更后 Insert 应拒(ErrProtocolIncompatible),却 err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("协议更新提交后 Insert 仍未完成")
	}

	// 不兼容账号绝不能落库。
	var landed int
	if err := pool.QueryRow(ctx,
		`SELECT count(*)::int FROM provider_accounts WHERE tenant_id=$1 AND channel_id=$2 AND deleted_at IS NULL`,
		seed.tenantID, seed.channelID,
	).Scan(&landed); err != nil {
		t.Fatalf("统计落库账号: %v", err)
	}
	if landed != 0 {
		t.Fatalf("协议已变更却落库 %d 个不兼容账号,期望 0", landed)
	}
}

// waitForBlockedAdvisoryLock 轮询 pg_locks 直到出现一个未授予的 advisory 锁,即某后端
// 正阻塞在 advisory lock 上(外部持锁方那把是 granted=true)。确定性替代 sleep。
func waitForBlockedAdvisoryLock(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx,
			`SELECT count(*)::int FROM pg_locks WHERE locktype='advisory' AND NOT granted`,
		).Scan(&waiting); err != nil {
			t.Fatalf("查询 pg_locks: %v", err)
		}
		if waiting >= 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("超时:未观测到 Insert 阻塞在 advisory lock 上")
		}
		select {
		case <-ctx.Done():
			t.Fatalf("ctx 取消: %v", ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func isMixedRiskConfirmRequired(err error) bool {
	return errors.Is(err, ErrMixedRiskConfirmRequired)
}

type twoProviderSeed struct {
	tenantID  int64
	providerA int64
	providerB int64
	channelID int64
	suffix    string
}

func seedTwoProviderChannel(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *twoProviderSeed {
	t.Helper()
	s := &twoProviderSeed{suffix: uuid.NewString()[:8]}
	u := s.suffix

	if err := pool.QueryRow(ctx, `INSERT INTO tenants (name) VALUES ($1) RETURNING id`, "ac-tenant-"+u).Scan(&s.tenantID); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = pool.Exec(bg, `DELETE FROM account_credentials WHERE tenant_id=$1`, s.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM provider_accounts WHERE tenant_id=$1`, s.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM channels WHERE tenant_id=$1`, s.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM pool_groups WHERE tenant_id=$1`, s.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM providers WHERE tenant_id=$1`, s.tenantID)
		_, _ = pool.Exec(bg, `DELETE FROM tenants WHERE id=$1`, s.tenantID)
	})

	newProvider := func(code string) int64 {
		var id int64
		if err := pool.QueryRow(ctx,
			`INSERT INTO providers (tenant_id, code, display_name, upstream_protocol)
			 VALUES ($1, $2, $3, 'openai_chat') RETURNING id`,
			s.tenantID, code+"-"+u, "Provider "+code+"-"+u,
		).Scan(&id); err != nil {
			t.Fatalf("seed provider %s: %v", code, err)
		}
		return id
	}
	s.providerA = newProvider("pa")
	s.providerB = newProvider("pb")

	var poolGroupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO pool_groups (tenant_id, name) VALUES ($1, $2) RETURNING id`, s.tenantID, "pg-"+u).Scan(&poolGroupID); err != nil {
		t.Fatalf("seed pool group: %v", err)
	}
	if err := pool.QueryRow(ctx,
		`INSERT INTO channels (tenant_id, pool_group_id, name) VALUES ($1, $2, $3) RETURNING id`,
		s.tenantID, poolGroupID, "ch-"+u,
	).Scan(&s.channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	return s
}

func openAccountCreatePool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("HUAKAI_DATABASE_URL")
	if dsn == "" {
		t.Skip("HUAKAI_DATABASE_URL 未设置,跳过 integration_pg 测试")
	}
	p, err := db.Open(ctx, db.PoolConfig{DSN: dsn})
	if err != nil {
		t.Fatalf("打开连接池: %v", err)
	}
	t.Cleanup(p.Close)
	return p
}
