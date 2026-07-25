// Package model 提供 GORM 数据模型和数据库操作。
//
// 支持数据库：
//   - SQLite（默认，零配置）
//   - MySQL（通过 DB_TYPE=mysql + DB_DSN 环境变量）
//   - PostgreSQL（通过 DB_TYPE=postgres + DB_DSN 环境变量）
package model

import (
	"log"
	"os"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 全局数据库实例。
var DB *gorm.DB

// InitDB 初始化数据库连接并执行自动迁移。
//
// 环境变量：
//   - DB_TYPE: 数据库类型（sqlite/mysql/postgres），默认 sqlite
//   - DB_DSN: 数据库连接字符串（mysql/postgres 必填）
//   - DB_PATH: SQLite 文件路径（仅 sqlite），默认 ragent_router.db
func InitDB(dbPath string) error {
	dbType := os.Getenv("DB_TYPE")
	if dbType == "" {
		dbType = "sqlite"
	}

	var dialector gorm.Dialector
	switch dbType {
	case "mysql":
		dsn := os.Getenv("DB_DSN")
		if dsn == "" {
			dsn = "root:123456@tcp(127.0.0.1:3306)/ragent_router?charset=utf8mb4&parseTime=True&loc=Local"
		}
		dialector = mysql.Open(dsn)
		log.Printf("[数据库] 使用 MySQL: %s", dsn[:min(30, len(dsn))]+"...")

	case "postgres":
		dsn := os.Getenv("DB_DSN")
		if dsn == "" {
			dsn = "host=127.0.0.1 user=postgres password=123456 dbname=ragent_router port=5432 sslmode=disable"
		}
		dialector = postgres.Open(dsn)
		log.Printf("[数据库] 使用 PostgreSQL")

	default: // sqlite
		dialector = sqlite.Open(dbPath)
		log.Printf("[数据库] 使用 SQLite: %s", dbPath)
	}

	var err error
	DB, err = gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return err
	}

	// 设置连接池
	sqlDB, err := DB.DB()
	if err != nil {
		return err
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)

	// SQLite 特有配置
	if dbType == "sqlite" {
		sqlDB.Exec("PRAGMA journal_mode=WAL")
		sqlDB.Exec("PRAGMA busy_timeout=5000")
	}

	// 自动迁移
	if err := DB.AutoMigrate(
		&User{},
		&Token{},
		&Channel{},
		&Ability{},
		&RequestLog{},
		&Option{},
		&RedemptionCode{},
	); err != nil {
		return err
	}

	// 创建默认 root 用户（如果不存在）
	var count int64
	DB.Model(&User{}).Count(&count)
	if count == 0 {
		hashedPwd, _ := HashPassword("123456")
		root := &User{
			Username: "root",
			Password: hashedPwd,
			Role:     RoleRootUser,
			Status:   UserStatusEnabled,
		}
		DB.Create(root)
		log.Println("[数据库] 已创建默认 root 用户 (密码: 123456)")
	}

	log.Println("[数据库] 初始化完成")
	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
