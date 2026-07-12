// Package dbmigrate 提供可选的进程内自迁移(默认关,经 HUAKAI_AUTO_MIGRATE 开启)。
//
// 它复用 golang-migrate,与 compose 的 migrate one-shot 共用同一张 schema_migrations 表
// (版本/dirty 语义一致),两路互相幂等、不撞表:谁先跑到最新,另一路再跑即"无变更"直接返回。
// 默认关时迁移仍外置(多副本部署由 one-shot 受控跑、避免竞态);开启用于裸二进制单实例,省去
// 运维"先手动跑迁移再起 gateway"那一步。
package dbmigrate

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	// 注册 postgres 数据库驱动(side-effect import),使 "postgres://" 目标可用。
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

// Up 把内嵌迁移 FS 里所有未应用的迁移升到最新。
//
//   - migrationsFS:内嵌迁移文件系统(根下含 migrations/ 子目录)。
//   - databaseURL:形如 postgres://user:pass@host:port/db?sslmode=...。
//
// golang-migrate 的 postgres 驱动在迁移期自动持有 advisory lock:多副本并发启动时,后到者
// 阻塞等锁,拿到锁后发现已是最新即返回"无变更"。无变更(ErrNoChange)视为成功。
func Up(migrationsFS fs.FS, databaseURL string) error {
	if databaseURL == "" {
		return errors.New("dbmigrate: databaseURL 为空")
	}
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("dbmigrate: 构建内嵌迁移源失败: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, databaseURL)
	if err != nil {
		return fmt.Errorf("dbmigrate: 初始化迁移器失败: %w", err)
	}
	// Close 释放 source 与数据库连接;迁移期的 advisory lock 在每次 Up/Down 内部成对获取释放。
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("dbmigrate: 应用迁移失败: %w", err)
	}
	return nil
}
