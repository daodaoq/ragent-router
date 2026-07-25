package common

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

// ────────────────────────────────────────────────────────────
// Elasticsearch 客户端（轻量级实现，不依赖第三方库）
//
// 面试考点：
//  1. 倒排索引原理？（Term → DocID 列表，支持全文检索）
//  2. 分词器？（Standard/IK/Whitespace，中文需要 IK 分词器）
//  3. DSL 查询？（match/term/range/bool/aggs）
//  4. 与 MySQL 全文索引的区别？（ES 专为搜索设计，支持分词、相关性评分、聚合）
//  5. 如何保证数据同步？（双写/CDC/Canal/Logstash）
// ────────────────────────────────────────────────────────────

// ESClient Elasticsearch 客户端。
type ESClient struct {
	baseURL    string
	httpClient *http.Client
	index      string
}

// ESResponse ES 查询响应。
type ESResponse struct {
	Took     int  `json:"took"`
	TimedOut bool `json:"timed_out"`
	Hits     struct {
		Total struct {
			Value    int    `json:"value"`
			Relation string `json:"relation"`
		} `json:"total"`
		Hits []ESHit `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations,omitempty"`
}

// ESHit ES 文档命中。
type ESHit struct {
	Index  string          `json:"_index"`
	Id     string          `json:"_id"`
	Score  float64         `json:"_score"`
	Source json.RawMessage `json:"_source"`
}

// NewESClient 创建 ES 客户端。
func NewESClient(baseURL, index string) *ESClient {
	return &ESClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		index: index,
	}
}

// 全局 ES 客户端
var ESClientInstance *ESClient

// InitElasticsearch 初始化 Elasticsearch。
func InitElasticsearch() {
	esURL := GetEnv("ELASTICSEARCH_URL", "")
	if esURL == "" {
		log.Println("[ES] 未配置 ELASTICSEARCH_URL，Elasticsearch 未启用")
		return
	}

	index := GetEnv("ELASTICSEARCH_INDEX", "ragent-logs")
	ESClientInstance = NewESClient(esURL, index)

	// 创建索引（如果不存在）
	if err := ESClientInstance.CreateIndex(context.Background()); err != nil {
		log.Printf("[ES] 创建索引失败: %v", err)
	}

	log.Printf("[ES] Elasticsearch 已连接: %s (index: %s)", esURL, index)
}

// CreateIndex 创建索引（带映射）。
func (c *ESClient) CreateIndex(ctx context.Context) error {
	mapping := map[string]interface{}{
		"settings": map[string]interface{}{
			"number_of_shards":   1,
			"number_of_replicas": 0,
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"prompt": map[string]interface{}{
					"type":     "text",
					"analyzer": "standard",
				},
				"model": map[string]interface{}{
					"type": "keyword",
				},
				"provider": map[string]interface{}{
					"type": "keyword",
				},
				"status": map[string]interface{}{
					"type": "keyword",
				},
				"cost_usd": map[string]interface{}{
					"type": "float",
				},
				"latency_ms": map[string]interface{}{
					"type": "long",
				},
				"total_tokens": map[string]interface{}{
					"type": "long",
				},
				"prompt_tokens": map[string]interface{}{
					"type": "long",
				},
				"completion_tokens": map[string]interface{}{
					"type": "long",
				},
				"route_reason": map[string]interface{}{
					"type": "text",
				},
				"error_detail": map[string]interface{}{
					"type": "text",
				},
				"created_at": map[string]interface{}{
					"type":   "date",
					"format": "epoch_millis||yyyy-MM-dd'T'HH:mm:ss",
				},
			},
		},
	}

	body, _ := json.Marshal(mapping)
	req, _ := http.NewRequestWithContext(ctx, "PUT", c.baseURL+"/"+c.index, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 400 表示索引已存在，忽略
	if resp.StatusCode == 400 {
		return nil
	}
	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ES 创建索引失败: %d %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// IndexDocument 索引文档。
func (c *ESClient) IndexDocument(ctx context.Context, id string, doc interface{}) error {
	if c == nil {
		return nil
	}

	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}

	url := fmt.Sprintf("%s/%s/_doc/%s", c.baseURL, c.index, id)
	req, _ := http.NewRequestWithContext(ctx, "PUT", url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ES 索引文档失败: %d %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// IndexDocumentBulk 批量索引文档。
func (c *ESClient) IndexDocumentBulk(ctx context.Context, docs []interface{}) error {
	if c == nil || len(docs) == 0 {
		return nil
	}

	var body bytes.Buffer
	for i, doc := range docs {
		// Bulk API 格式：action_line\ndata_line\n
		action := map[string]interface{}{
			"index": map[string]interface{}{
				"_index": c.index,
				"_id":    fmt.Sprintf("bulk_%d_%d", time.Now().UnixMilli(), i),
			},
		}
		actionJSON, _ := json.Marshal(action)
		body.Write(actionJSON)
		body.WriteByte('\n')

		docJSON, _ := json.Marshal(doc)
		body.Write(docJSON)
		body.WriteByte('\n')
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/_bulk", &body)
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ES 批量索引失败: %d %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// Search 全文搜索。
func (c *ESClient) Search(ctx context.Context, query map[string]interface{}, from, size int) (*ESResponse, error) {
	if c == nil {
		return nil, nil
	}

	// 构建查询体
	searchBody := map[string]interface{}{
		"query": query,
		"from":  from,
		"size":  size,
		"sort": []map[string]interface{}{
			{"created_at": map[string]string{"order": "desc"}},
		},
	}

	body, _ := json.Marshal(searchBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+c.index+"/_search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ESResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// SearchByPrompt 按 prompt 全文搜索。
func (c *ESClient) SearchByPrompt(ctx context.Context, keyword string, from, size int) (*ESResponse, error) {
	query := map[string]interface{}{
		"bool": map[string]interface{}{
			"must": []map[string]interface{}{
				{
					"match": map[string]interface{}{
						"prompt": keyword,
					},
				},
			},
		},
	}
	return c.Search(ctx, query, from, size)
}

// Aggregate 聚合查询。
func (c *ESClient) Aggregate(ctx context.Context, aggs map[string]interface{}) (*ESResponse, error) {
	if c == nil {
		return nil, nil
	}

	searchBody := map[string]interface{}{
		"size": 0, // 不需要文档，只要聚合结果
		"aggs": aggs,
	}

	body, _ := json.Marshal(searchBody)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/"+c.index+"/_search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result ESResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

// AggregateByModel 按模型聚合统计。
func (c *ESClient) AggregateByModel(ctx context.Context) (*ESResponse, error) {
	aggs := map[string]interface{}{
		"by_model": map[string]interface{}{
			"terms": map[string]interface{}{
				"field": "model",
				"size":  20,
			},
			"aggs": map[string]interface{}{
				"avg_latency": map[string]interface{}{
					"avg": map[string]interface{}{
						"field": "latency_ms",
					},
				},
				"total_cost": map[string]interface{}{
					"sum": map[string]interface{}{
						"field": "cost_usd",
					},
				},
			},
		},
	}
	return c.Aggregate(ctx, aggs)
}

// AggregateByProvider 按提供商聚合统计。
func (c *ESClient) AggregateByProvider(ctx context.Context) (*ESResponse, error) {
	aggs := map[string]interface{}{
		"by_provider": map[string]interface{}{
			"terms": map[string]interface{}{
				"field": "provider",
				"size":  20,
			},
			"aggs": map[string]interface{}{
				"avg_latency": map[string]interface{}{
					"avg": map[string]interface{}{
						"field": "latency_ms",
					},
				},
				"error_count": map[string]interface{}{
					"filter": map[string]interface{}{
						"term": map[string]interface{}{
							"status": "error",
						},
					},
				},
			},
		},
	}
	return c.Aggregate(ctx, aggs)
}

// AggregateByTime 按时间聚合（日期直方图）。
func (c *ESClient) AggregateByTime(ctx context.Context, interval string) (*ESResponse, error) {
	aggs := map[string]interface{}{
		"over_time": map[string]interface{}{
			"date_histogram": map[string]interface{}{
				"field":    "created_at",
				"interval": interval,
			},
			"aggs": map[string]interface{}{
				"total_cost": map[string]interface{}{
					"sum": map[string]interface{}{
						"field": "cost_usd",
					},
				},
				"avg_latency": map[string]interface{}{
					"avg": map[string]interface{}{
						"field": "latency_ms",
					},
				},
			},
		},
	}
	return c.Aggregate(ctx, aggs)
}
