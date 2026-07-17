package adminuserhttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	admindb "github.com/BloomingProsperity/HUAKAI/internal/db/admin"
	"github.com/BloomingProsperity/HUAKAI/internal/userauth"
)

// user_create.go — admin 创建终端用户(S4)。永远建为 role='user',禁 'admin'
// (越权提权护栏);密码 argon2id 散列、永不存明文;email NormalizeEmail 化 +
// 租户内唯一冲突映 409;创建即审计(create_user)。租户隔离经 resolveTenantIdentity。

// createUserMinPasswordLen 是创建口令最短长度。HUAKAI 取 8；弱口令返回 400 weak_password。
const createUserMinPasswordLen = 8

// ErrUserAlreadyExists 由 store 在租户内同邮箱(未软删)唯一冲突时返回,
// handler 映射为 409 admin_user_exists。
var ErrUserAlreadyExists = errors.New("adminuserhttp: user already exists")

// userCreateService 建用户(已散列口令、已规范化邮箱),实现负责把唯一冲突
// 翻成 ErrUserAlreadyExists。返回新用户的稳定字段供 201 响应体。
type userCreateService interface {
	CreateUser(ctx context.Context, in userCreateInput) (userCreated, error)
}

// userCreateInput 是传给 store 的已校验入参。Role 恒为 'user'(handler 强制),
// PasswordHash 已是 argon2id 编码串(明文不进 store)。
type userCreateInput struct {
	TenantID     int64
	Email        string
	DisplayName  string
	PasswordHash string
	Role         string
}

// userCreated 是 store 建成后回填的稳定字段(供响应体)。
type userCreated struct {
	ID        int64
	Email     string
	Role      string
	Status    string
	CreatedAt string
}

type createUserRequest struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

// setUserCreateRequest 解码 + 校验创建请求,产出已散列入参。校验顺序刻意为:
// JSON → email → password 强度 → role 白名单(role!='user' → 403 admin_role_forbidden,
// 比 400 更准确表达「越权提权被拒」而非「字段格式错」)。
func setUserCreateRequest(w http.ResponseWriter, r *http.Request, tenantID int64) (userCreateInput, bool) {
	var req createUserRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "request body must be valid JSON")
		return userCreateInput{}, false
	}
	email := userauth.NormalizeEmail(req.Email)
	if email == "" || !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n") {
		writeError(w, http.StatusBadRequest, "invalid_email", "email must be a non-empty address")
		return userCreateInput{}, false
	}
	if len(req.Password) < createUserMinPasswordLen {
		writeError(w, http.StatusBadRequest, "weak_password",
			fmt.Sprintf("password must be at least %d characters", createUserMinPasswordLen))
		return userCreateInput{}, false
	}
	// role 缺省 'user';任何非 'user' 值(含 'admin')→ 403 越权护栏。
	// 这是越权提权(CMB-5)的核心守卫:本面绝不能建 admin。
	role := strings.TrimSpace(req.Role)
	if role == "" {
		role = "user"
	}
	if role != "user" {
		writeError(w, http.StatusForbidden, "admin_role_forbidden",
			"cannot create a user with elevated role via this endpoint")
		return userCreateInput{}, false
	}
	hash, err := userauth.HashPassword(req.Password, userauth.DefaultPasswordPolicy())
	if err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", "password rejected")
		return userCreateInput{}, false
	}
	return userCreateInput{
		TenantID:     tenantID,
		Email:        email,
		DisplayName:  strings.TrimSpace(req.DisplayName),
		PasswordHash: hash,
		Role:         role,
	}, true
}

// newCreateUserHandler 创建终端用户(POST /admin/v1/users)。
func newCreateUserHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ident, tenantID, ok := resolveTenantIdentity(w, r, d)
		if !ok {
			return
		}
		if d.UserCreator == nil || d.Audit == nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_not_configured",
				"admin user-create dependency unset")
			return
		}
		in, ok := setUserCreateRequest(w, r, tenantID)
		if !ok {
			return
		}
		created, err := d.UserCreator.CreateUser(r.Context(), in)
		if errors.Is(err, ErrUserAlreadyExists) {
			writeError(w, http.StatusConflict, "admin_user_exists",
				"a user with this email already exists in the tenant")
			return
		}
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("create user failed: %v", err))
			return
		}
		ai := buildUnlockAuditInput(r, ident, "")
		payload, err := json.Marshal(map[string]string{"email": created.Email, "role": created.Role})
		if err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("marshal create audit failed: %v", err))
			return
		}
		audit := admindb.InsertAdminAuditEventParams{
			TenantID: &tenantID, ActorID: ai.ActorID, ActorRole: ai.ActorRole,
			Action: "create_user", TargetType: "user", TargetID: &created.ID, RequestID: ai.RequestID,
			Payload: payload,
		}
		if _, err := d.Audit.InsertAdminAuditEvent(r.Context(), audit); err != nil {
			writeError(w, http.StatusServiceUnavailable, "admin_users_backend_error",
				fmt.Sprintf("write create audit failed: %v", err))
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"id":         created.ID,
			"email":      created.Email,
			"role":       created.Role,
			"status":     created.Status,
			"created_at": created.CreatedAt,
		})
	}
}

// postgresUserCreateStore 经 userauth.PostgresStore.CreateUser 建用户(active +
// email_verified=true,因 admin 直建无需自助验证)并把 23505 唯一冲突翻成
// ErrUserAlreadyExists。users.role 由 DB 默认值 'user' 满足本面(CreateUser 不收
// role 参数;本面恒建 user,故无需显式设)。
type postgresUserCreateStore struct {
	pool *pgxpool.Pool
}

// NewPostgresUserCreateStore 接线 admin 用户创建 store。
func NewPostgresUserCreateStore(pool *pgxpool.Pool) userCreateService {
	if pool == nil {
		return nil
	}
	return postgresUserCreateStore{pool: pool}
}

func (s postgresUserCreateStore) CreateUser(ctx context.Context, in userCreateInput) (userCreated, error) {
	if s.pool == nil {
		return userCreated{}, userauth.ErrStoreNotConfigured
	}
	user, err := userauth.NewPostgresStore(s.pool).CreateUser(ctx, userauth.CreateUserParams{
		TenantID:      in.TenantID,
		Email:         in.Email,
		DisplayName:   in.DisplayName,
		PasswordHash:  in.PasswordHash,
		EmailVerified: true,
		Status:        userauth.UserStatusActive,
	})
	if err != nil {
		if isUserUniqueViolation(err) {
			return userCreated{}, ErrUserAlreadyExists
		}
		return userCreated{}, err
	}
	return userCreated{
		ID:        user.ID,
		Email:     user.Email,
		Role:      "user",
		Status:    string(user.Status),
		CreatedAt: timestamp(user.CreatedAt),
	}, nil
}

func isUserUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
