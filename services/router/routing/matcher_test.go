package routing

import (
	"context"
	"testing"

	"github.com/ragent/router/shared/model"
)

// =============================================================================
// 关键词匹配器测试
// =============================================================================

func TestRuleEngine_BasicMatch(t *testing.T) {
	providers := map[string]*model.Config{
		"DeepSeek": {Name: "DeepSeek", Enabled: true},
		"Claude":   {Name: "Claude", Enabled: true},
	}

	rules := []Rule{
		{Keywords: []string{"代码", "编程", "debug"}, Provider: "DeepSeek"},
		{Keywords: []string{"写作", "翻译", "创意"}, Provider: "Claude"},
	}

	engine := NewRuleEngine(rules, providers, "DeepSeek")

	tests := []struct {
		prompt   string
		expected string
	}{
		{"帮我写代码", "DeepSeek"},
		{"翻译这段话", "Claude"},
	}

	for _, tt := range tests {
		result := engine.Match(context.Background(), tt.prompt, "")
		if result == nil {
			t.Errorf("prompt=%q: result 不应为 nil", tt.prompt)
			continue
		}
		if result.Name != tt.expected {
			t.Errorf("prompt=%q: want=%s, got=%s", tt.prompt, tt.expected, result.Name)
		}
	}
}

func TestRuleEngine_NoMatch(t *testing.T) {
	providers := map[string]*model.Config{
		"DeepSeek": {Name: "DeepSeek", Enabled: true},
	}
	rules := []Rule{
		{Keywords: []string{"代码"}, Provider: "DeepSeek"},
	}
	engine := NewRuleEngine(rules, providers, "DeepSeek")

	// 无匹配时返回 nil（由 HybridRouter 继续尝试其他策略）
	result := engine.Match(context.Background(), "今天天气怎么样", "")
	if result != nil {
		t.Errorf("无匹配时应返回 nil, got=%v", result)
	}
}

func TestRuleEngine_DisabledProvider(t *testing.T) {
	providers := map[string]*model.Config{
		"DeepSeek": {Name: "DeepSeek", Enabled: false}, // 禁用
	}
	rules := []Rule{
		{Keywords: []string{"代码"}, Provider: "DeepSeek"},
	}
	engine := NewRuleEngine(rules, providers, "DeepSeek")

	// 供应商禁用时应返回 nil
	result := engine.Match(context.Background(), "帮我写代码", "")
	if result != nil {
		t.Errorf("禁用供应商应返回 nil, got=%v", result)
	}
}

func TestRuleEngine_ModelMatch(t *testing.T) {
	providers := map[string]*model.Config{
		"DeepSeek": {Name: "DeepSeek", Enabled: true},
	}
	engine := NewRuleEngine(nil, providers, "DeepSeek")

	// 指定模型时，按模型名匹配供应商
	result := engine.Match(context.Background(), "hello", "deepseek-chat")
	if result == nil || result.Name != "DeepSeek" {
		t.Errorf("模型匹配: want=DeepSeek, got=%v", result)
	}
}

// =============================================================================
// 并发安全测试
// =============================================================================

func TestRuleEngine_Concurrent(t *testing.T) {
	providers := map[string]*model.Config{
		"DeepSeek": {Name: "DeepSeek", Enabled: true},
	}
	rules := []Rule{
		{Keywords: []string{"代码"}, Provider: "DeepSeek"},
	}
	engine := NewRuleEngine(rules, providers, "DeepSeek")

	done := make(chan bool)
	for i := 0; i < 100; i++ {
		go func() {
			_ = engine.Match(context.Background(), "帮我写代码", "")
			done <- true
		}()
	}
	for i := 0; i < 100; i++ {
		<-done
	}
}
