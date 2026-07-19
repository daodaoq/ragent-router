package api

import (
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/google/uuid"
)

// ── Recovery ──────────────────────────────────────────────────────────

// Recovery 捕获 handler panic，防止整个服务崩溃。
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("[PANIC] %v\n%s", err, debug.Stack())
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// ── Request ID ────────────────────────────────────────────────────────

// RequestID 为每个请求生成唯一 ID 并写入响应头。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := r.Header.Get("X-Request-ID")
		if reqID == "" {
			reqID = uuid.NewString()
		}
		w.Header().Set("X-Request-ID", reqID)
		next.ServeHTTP(w, r)
	})
}

// ── Auth ──────────────────────────────────────────────────────────────

// Auth 可选 API Token 认证。通过 RAGENT_API_TOKEN 环境变量配置。
func Auth(next http.Handler) http.Handler {
	token := os.Getenv("RAGENT_API_TOKEN")
	if token == "" {
		return next // 未配置 token，跳过认证
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			next.ServeHTTP(w, r)
			return
		}
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") || strings.TrimPrefix(authHeader, "Bearer ") != token {
			if r.Header.Get("X-Api-Key") != token {
				WriteError(w, http.StatusUnauthorized, "unauthorized")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ── CORS ──────────────────────────────────────────────────────────────

// CORS 添加跨域访问头。通过 CORS_ALLOW_ALL 环境变量切换开发/生产模式。
func CORS(allowedOrigins map[string]bool) func(http.Handler) http.Handler {
	allowAll := os.Getenv("CORS_ALLOW_ALL") == "true"

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" {
				if allowAll || allowedOrigins[origin] {
					w.Header().Set("Access-Control-Allow-Origin", origin)
				} else {
					w.Header().Set("Access-Control-Allow-Origin", "")
					log.Printf("[CORS] 拒绝未知来源: %s", origin)
				}
			} else {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Api-Key, Anthropic-Version")
			w.Header().Set("Access-Control-Expose-Headers", "X-Ragent-Provider, X-Ragent-Model, X-Ragent-Reason")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// DefaultCORSOrigins 返回开发环境的 CORS 白名单。
func DefaultCORSOrigins() map[string]bool {
	return map[string]bool{
		"http://localhost:5173":  true,
		"http://localhost:15722": true,
		"http://127.0.0.1:5173":  true,
		"http://127.0.0.1:15722": true,
	}
}
