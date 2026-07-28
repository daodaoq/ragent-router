package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"
)

// ── 语义缓存存储 ──────────────────────────────────────────────────────

// SemanticCacheStore 管理语义缓存的持久化。
// 将用户 prompt 的 embedding 向量与历史响应关联，
// 通过余弦相似度比对实现语义级缓存命中。
type SemanticCacheStore struct {
	db          *sql.DB
	simThreshold float64 // 余弦相似度阈值（0.0-1.0）
	maxEntries   int     // 最大缓存条目数
}

// NewSemanticCacheStore 创建语义缓存存储。
func NewSemanticCacheStore(db *sql.DB, simThreshold float64, maxEntries int) (*SemanticCacheStore, error) {
	if err := migrateSemanticCache(db); err != nil {
		return nil, fmt.Errorf("migrate semantic cache: %w", err)
	}
	return &SemanticCacheStore{
		db:           db,
		simThreshold: simThreshold,
		maxEntries:   maxEntries,
	}, nil
}

// SemCacheEntry 是一条缓存的响应记录。
type SemCacheEntry struct {
	ID              int64     `json:"id"`
	PromptEmbedding []float64 `json:"-"` // 不在 JSON 中展示
	PromptText      string    `json:"prompt_text"`
	Response        string    `json:"response"`
	Provider        string    `json:"provider"`
	Model           string    `json:"model"`
	Tokens          int       `json:"tokens"`
	Similarity      float64   `json:"similarity"` // 命中时的相似度
	CreatedAt       time.Time `json:"created_at"`
}

// SemCacheStats 是缓存统计。
type SemCacheStats struct {
	TotalEntries int64 `json:"total_entries"`
	HitsToday    int64 `json:"hits_today"`
	MissesToday  int64 `json:"misses_today"`
	HitRate      float64 `json:"hit_rate"` // 百分比
}

// Insert 写入一条缓存记录。超出 maxEntries 时淘汰最早的条目。
func (s *SemanticCacheStore) Insert(entry *SemCacheEntry) error {
	embBytes, err := json.Marshal(entry.PromptEmbedding)
	if err != nil {
		return fmt.Errorf("marshal embedding: %w", err)
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO semantic_cache (prompt_embedding, prompt_text, response, provider, model, tokens, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, embBytes, entry.PromptText, entry.Response, entry.Provider, entry.Model, entry.Tokens, time.Now())
	if err != nil {
		return fmt.Errorf("insert cache: %w", err)
	}

	// 超出上限时淘汰最早条目
	var count int64
	tx.QueryRow("SELECT COUNT(1) FROM semantic_cache").Scan(&count)
	if count > int64(s.maxEntries) {
		toDelete := count - int64(s.maxEntries)
		tx.Exec("DELETE FROM semantic_cache WHERE id IN (SELECT id FROM semantic_cache ORDER BY created_at ASC LIMIT ?)", toDelete)
	}

	return tx.Commit()
}

// FindSimilar 遍历所有缓存条目，返回余弦相似度最高的匹配（需超过阈值）。
// 时间复杂度 O(N×D)，N 为缓存条目数，D 为向量维度。
// 缓存条目超过 10000 时建议改用向量索引（如 FAISS/HNSW）。
func (s *SemanticCacheStore) FindSimilar(promptEmbedding []float64) (*SemCacheEntry, bool) {
	rows, err := s.db.Query(`
		SELECT id, prompt_embedding, prompt_text, response, provider, model, tokens, created_at
		FROM semantic_cache ORDER BY created_at DESC LIMIT ?
	`, s.maxEntries)
	if err != nil {
		return nil, false
	}
	defer rows.Close()

	var best *SemCacheEntry
	bestSim := s.simThreshold // 必须超过阈值

	for rows.Next() {
		var id, tokens int64
		var embBytes []byte
		var promptText, response, provider, model string
		var createdAt time.Time

		if err := rows.Scan(&id, &embBytes, &promptText, &response, &provider, &model, &tokens, &createdAt); err != nil {
			continue
		}

		var cachedEmb []float64
		if err := json.Unmarshal(embBytes, &cachedEmb); err != nil {
			continue
		}

		sim := cosineSimilarity(promptEmbedding, cachedEmb)
		if sim > bestSim {
			bestSim = sim
			best = &SemCacheEntry{
				ID:              id,
				PromptEmbedding: cachedEmb,
				PromptText:      promptText,
				Response:        response,
				Provider:        provider,
				Model:           model,
				Tokens:          int(tokens),
				Similarity:      sim,
				CreatedAt:       createdAt,
			}
		}
	}

	if best != nil {
		// 记录命中
		s.db.Exec("INSERT INTO sem_cache_stats (hit, created_at) VALUES (1, ?)", time.Now())
		return best, true
	}

	// 记录未命中
	s.db.Exec("INSERT INTO sem_cache_stats (hit, created_at) VALUES (0, ?)", time.Now())
	return nil, false
}

// EvictExpired 清理过期的缓存条目（超过 24 小时）。
func (s *SemanticCacheStore) EvictExpired() (int64, error) {
	result, err := s.db.Exec(
		"DELETE FROM semantic_cache WHERE created_at < ?",
		time.Now().Add(-24*time.Hour),
	)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Clear 清空所有缓存。
func (s *SemanticCacheStore) Clear() error {
	_, err := s.db.Exec("DELETE FROM semantic_cache")
	return err
}

// Count 返回当前缓存条目数。
func (s *SemanticCacheStore) Count() (int64, error) {
	var n int64
	err := s.db.QueryRow("SELECT COUNT(1) FROM semantic_cache").Scan(&n)
	return n, err
}

// Stats 返回缓存命中统计（今日）。
func (s *SemanticCacheStore) Stats() (*SemCacheStats, error) {
	today := time.Now().Truncate(24 * time.Hour)
	var stats SemCacheStats

	s.db.QueryRow("SELECT COUNT(1) FROM semantic_cache").Scan(&stats.TotalEntries)
	s.db.QueryRow("SELECT COUNT(1) FROM sem_cache_stats WHERE hit=1 AND created_at >= ?", today).Scan(&stats.HitsToday)
	s.db.QueryRow("SELECT COUNT(1) FROM sem_cache_stats WHERE hit=0 AND created_at >= ?", today).Scan(&stats.MissesToday)

	total := stats.HitsToday + stats.MissesToday
	if total > 0 {
		stats.HitRate = float64(stats.HitsToday) / float64(total) * 100
	}
	return &stats, nil
}

// ── 余弦相似度（纯 Go，与 routing/embedding.go 保持一致）──

func cosineSimilarity(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ── 迁移 ──────────────────────────────────────────────────────────────

func migrateSemanticCache(db *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS semantic_cache (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		prompt_embedding BLOB NOT NULL,
		prompt_text TEXT DEFAULT '',
		response TEXT NOT NULL,
		provider TEXT DEFAULT '',
		model TEXT DEFAULT '',
		tokens INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_sem_cache_created ON semantic_cache(created_at);

	CREATE TABLE IF NOT EXISTS sem_cache_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hit INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_cache_stats_created ON sem_cache_stats(created_at);
	`
	_, err := db.Exec(schema)
	return err
}
