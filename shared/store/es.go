// ESLogStore 是请求日志的 Elasticsearch 持久化存储。
//
// 使用 ES REST API（net/http），不依赖第三方 ES SDK，
// 减少依赖体积，适合轻量级集成。
//
// 面试考点：
//   - 为什么用 ES？全文检索（按 prompt 模糊搜索）、聚合分析（按模型/供应商统计）
//   - Bulk API 批量写入：减少网络 RTT，提升写入吞吐
//   - 别名（Alias）+ 索引模板：按月滚动索引，自动清理过期数据
package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// ESLogStore Elasticsearch 存储。
type ESLogStore struct {
	baseURL    string       // ES 地址（如 "http://localhost:9200"）
	index      string       // 索引名（如 "ragent-request-logs"）
	httpClient *http.Client
}

// NewESLogStore 创建 ES 存储。
//
// 参数：
//   - baseURL: ES 地址（如 "http://localhost:9200"）
//   - index: 索引名（如 "ragent-request-logs"）
func NewESLogStore(baseURL, index string) *ESLogStore {
	if index == "" {
		index = "ragent-request-logs"
	}
	return &ESLogStore{
		baseURL: strings.TrimRight(baseURL, "/"),
		index:   index,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 5,
				IdleConnTimeout:     30 * time.Second,
			},
		},
	}
}

// ESDocument ES 文档结构（与 RequestLogRecord 对应）。
type ESDocument struct {
	ID                  string  `json:"id"`
	Prompt              string  `json:"prompt"`
	PromptTokens        int     `json:"prompt_tokens"`
	CompletionTokens    int     `json:"completion_tokens"`
	TotalTokens         int     `json:"total_tokens"`
	Model               string  `json:"model"`
	Provider            string  `json:"provider"`
	Status              string  `json:"status"`
	ErrorDetail         string  `json:"error_detail,omitempty"`
	CostUSD             float64 `json:"cost_usd"`
	LatencyMs           int64   `json:"latency_ms"`
	RouteReason         string  `json:"route_reason,omitempty"`
	UpstreamURL         string  `json:"upstream_url,omitempty"`
	UpstreamRequestID   string  `json:"upstream_request_id,omitempty"`
	Timestamp           string  `json:"@timestamp"`
}

// Index 索引单条文档。
func (s *ESLogStore) Index(ctx context.Context, doc *ESDocument) error {
	if doc.Timestamp == "" {
		doc.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	body, err := json.Marshal(doc)
	if err != nil {
		return fmt.Errorf("marshal doc: %w", err)
	}

	url := fmt.Sprintf("%s/%s/_doc/%s", s.baseURL, s.index, doc.ID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("es index: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) // 消费响应体

	if resp.StatusCode >= 400 {
		return fmt.Errorf("es index: status %d", resp.StatusCode)
	}
	return nil
}

// BulkIndex 批量索引文档（使用 ES Bulk API）。
//
// 面试考点：
//   - Bulk API 将多个操作合并为一次 HTTP 请求，减少网络 RTT
//   - 每行一个 action + source，最后必须以 \n 结尾
//   - 批量大小建议 500-5000 条，太大导致 GC 压力
func (s *ESLogStore) BulkIndex(ctx context.Context, docs []*ESDocument) error {
	if len(docs) == 0 {
		return nil
	}

	var buf bytes.Buffer
	now := time.Now().UTC().Format(time.RFC3339)

	for _, doc := range docs {
		if doc.Timestamp == "" {
			doc.Timestamp = now
		}

		// action 行
		action := map[string]interface{}{
			"index": map[string]interface{}{
				"_index": s.index,
				"_id":    doc.ID,
			},
		}
		actionBytes, _ := json.Marshal(action)
		buf.Write(actionBytes)
		buf.WriteByte('\n')

		// source 行
		sourceBytes, _ := json.Marshal(doc)
		buf.Write(sourceBytes)
		buf.WriteByte('\n')
	}

	url := fmt.Sprintf("%s/_bulk", s.baseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return fmt.Errorf("create bulk request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("es bulk: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Errors bool `json:"errors"`
		Items  []struct {
			Index struct {
				Status int   `json:"status"`
				Error  *struct{} `json:"error,omitempty"`
			} `json:"index"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode bulk response: %w", err)
	}

	if result.Errors {
		errCount := 0
		for _, item := range result.Items {
			if item.Index.Status >= 400 {
				errCount++
			}
		}
		log.Printf("[ES] BulkIndex 部分失败: %d/%d 条", errCount, len(docs))
	}

	return nil
}

// Search 搜索文档（简单全文搜索）。
func (s *ESLogStore) Search(ctx context.Context, query string, limit int) ([]ESDocument, error) {
	if limit <= 0 {
		limit = 20
	}

	searchBody := map[string]interface{}{
		"query": map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  query,
				"fields": []string{"prompt", "model", "provider", "route_reason"},
			},
		},
		"size": limit,
		"sort": []map[string]interface{}{
			{"@timestamp": "desc"},
		},
	}

	body, _ := json.Marshal(searchBody)
	url := fmt.Sprintf("%s/%s/_search", s.baseURL, s.index)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create search request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("es search: %w", err)
	}
	defer resp.Body.Close()

	var result struct {
		Hits struct {
			Hits []struct {
				Source ESDocument `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode search response: %w", err)
	}

	var docs []ESDocument
	for _, hit := range result.Hits.Hits {
		docs = append(docs, hit.Source)
	}
	return docs, nil
}

// Ping 检查 ES 连接。
func (s *ESLogStore) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, s.baseURL, nil)
	if err != nil {
		return err
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("es ping: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("es ping: status %d", resp.StatusCode)
	}
	return nil
}
