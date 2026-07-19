// Package api 提供 HTTP 层的通用工具：响应写入、中间件、路由注册。
package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// WriteJSON 以 JSON 格式写回响应。
// 先编码到 buffer 再 WriteHeader，避免编码失败时客户端收到空 body + 200 状态码。
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	if data == nil {
		w.WriteHeader(status)
		return
	}
	body, err := json.Marshal(data)
	if err != nil {
		log.Printf("[api] JSON 编码失败: %v", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(status)
	w.Write(body)
}

// WriteError 以标准格式写回错误响应。
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, map[string]string{"error": msg})
}
