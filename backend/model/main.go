// Package model 提供 GORM 数据模型和数据库操作。
package model

import (
	"log"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DB 全局数据库实例。
var DB *gorm.DB

// InitDB 初始化数据库连接并执行自动迁移。
func InitDB(dbPath string) error {
	var err error
	DB, err = gorm.Open(sqlite.Open(dbPath), &gorm.Config{
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
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(2)

	// 启用 WAL 模式
	sqlDB.Exec("PRAGMA journal_mode=WAL")
	sqlDB.Exec("PRAGMA busy_timeout=5000")

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
