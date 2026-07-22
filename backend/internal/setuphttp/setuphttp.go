// Package setuphttp 提供首装向导端点:全新部署在工作租户尚无管理员时,引导创建第一个
// 管理员账号;一旦管理员存在,安装端点永久拒绝(fail-closed),状态端点只读可公开探测。
package setuphttp

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

const (
	SetupTokenEnv       = "HUAKAI_SETUP_TOKEN"
	SetupTokenHeader    = "X-HUAKAI-Setup-Token"
	MinSetupTokenLength = 32
)

// Deps 注入 DB 池与工作租户;密码策略用 userauth 默认(与注册同源,避免两套成本参数)。
type Deps struct {
	Pool       *pgxpool.Pool
	TenantID   int64
	SetupToken string
}

// installGate 限制同时在算 argon2id 的安装请求数:该端点匿名可达,未安装窗口内
// 若不设闸,并发请求每个 64MiB 内存足以把全新部署打 OOM;超出直接 429。
var installGate = make(chan struct{}, 2)

// Mount 挂载 /setup/status 与 /setup/install。status 只读公开；install 需要部署者
// 持有的首装令牌，并在事务 advisory lock 内二次判定防并发双装。
func Mount(r chi.Router, d Deps) {
	r.Get("/setup/status", d.handleStatus)
	r.Post("/setup/install", d.handleInstall)
}

type statusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

func (d Deps) handleStatus(w http.ResponseWriter, r *http.Request) {
	if d.Pool == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_status_unavailable")
		return
	}
	needs, err := d.needsSetup(r.Context(), d.Pool)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "setup_status_unavailable")
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{NeedsSetup: needs})
}

// needsSetup:工作租户不存在未软删的 admin 用户即需要安装。HUAKAI 的部署配置
// 全在环境变量里,没有可查的安装标记文件,故以数据库事实为唯一判定源。
func (d Deps) needsSetup(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
SELECT EXISTS(
    SELECT 1 FROM users
    WHERE tenant_id = $1 AND role = 'admin' AND deleted_at IS NULL)`, d.TenantID).Scan(&exists)
	if err != nil {
		return false, err
	}
	return !exists, nil
}

type installRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

type installResponse struct {
	Email string `json:"email"`
}

func (d Deps) handleInstall(w http.ResponseWriter, r *http.Request) {
	if len(d.SetupToken) < MinSetupTokenLength {
		writeError(w, http.StatusServiceUnavailable, "setup_token_not_configured")
		return
	}
	if !setupTokenEqual(r.Header.Get(SetupTokenHeader), d.SetupToken) {
		writeError(w, http.StatusUnauthorized, "invalid_setup_token")
		return
	}
	var req installRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	email, err := userauth.ValidateNewUserEmail(req.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_email")
		return
	}
	if err := userauth.ValidateNewUserPassword(req.Password); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password")
		return
	}
	name, err := userauth.ValidateOptionalDisplayName(req.DisplayName)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_display_name")
		return
	}
	if name == "" {
		name = "Administrator"
	}

	// 哈希前先做一次廉价预检:已安装立刻 403;池缺失或探测失败一律 fail-closed,
	// 绝不在状态未知时白烧 argon2id(64MiB/次)。竞态由事务锁内的二次判定兜底。
	if d.Pool == nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable")
		return
	}
	needs, err := d.needsSetup(r.Context(), d.Pool)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "setup_unavailable")
		return
	}
	if !needs {
		writeError(w, http.StatusForbidden, "already_installed")
		return
	}

	// 并发闸:同时最多 2 个请求在算哈希,满了 429——防匿名并发把内存打爆。
	select {
	case installGate <- struct{}{}:
		defer func() { <-installGate }()
	default:
		writeError(w, http.StatusTooManyRequests, "setup_busy")
		return
	}

	// 哈希放在事务外:argon2id 是刻意昂贵的计算,不该占着连接与锁。
	hash, err := userauth.HashPassword(req.Password, userauth.DefaultPasswordPolicy())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash_failed")
		return
	}

	status, code := d.install(r.Context(), email, name, hash)
	if status != http.StatusCreated {
		writeError(w, status, code)
		return
	}
	writeJSON(w, http.StatusCreated, installResponse{Email: email})
}

func setupTokenEqual(got, want string) bool {
	gotDigest := sha256.Sum256([]byte(got))
	wantDigest := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(gotDigest[:], wantDigest[:]) == 1
}

// install 在单事务内:advisory lock 串行化 → 锁内重查守卫(TOCTOU)→ 建 admin。
// 返回 (HTTP 状态, 错误码);成功返回 (201, "")。
func (d Deps) install(ctx context.Context, email, name, hash string) (int, string) {
	if d.Pool == nil {
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	tx, err := d.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended('setup_install'::text, 0))`); err != nil {
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	needs, err := d.needsSetup(ctx, tx)
	if err != nil {
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	if !needs {
		// 已有管理员:安装通道永久关死。
		return http.StatusForbidden, "already_installed"
	}

	// 邮箱撞已有普通用户时不静默提升(提升语义走 HUAKAI_ADMIN_BOOTSTRAP_EMAIL),明确报冲突。
	if _, err := tx.Exec(ctx, `
INSERT INTO users (tenant_id, email, display_name, password_hash, email_verified, status, role)
VALUES ($1, $2, $3, $4, true, 'active', 'admin')`,
		d.TenantID, email, name, hash); err != nil {
		if isUniqueViolation(err) {
			return http.StatusConflict, "email_taken"
		}
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	if err := tx.Commit(ctx); err != nil {
		return http.StatusServiceUnavailable, "setup_unavailable"
	}
	return http.StatusCreated, ""
}

func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 走网关统一错误信封 {error:{code,message}},前端 lib/api 按 code 精确分支。
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": code}})
}
