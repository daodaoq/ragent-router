// Package common 提供项目共用的工具函数。
package utils

import (
	"encoding/json"
	"log"
)

// Unmarshal 是 json.Unmarshal 的封装，统一错误处理。
func Unmarshal(data []byte, v interface{}) error {
	return json.Unmarshal(data, v)
}

// Marshal 是 json.Marshal 的封装，统一错误处理。
func Marshal(v interface{}) ([]byte, error) {
	return json.Marshal(v)
}

// SysLog 记录系统日志。
func SysLog(format string, v ...interface{}) {
	log.Printf("[SYS] "+format, v...)
}
