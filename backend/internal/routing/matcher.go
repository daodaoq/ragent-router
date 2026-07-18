// Package routing provides request-to-provider routing logic.
// The rule engine matches user prompts against configured patterns
// and selects the appropriate AI provider.
package routing

import (
	"sort"
	"strings"

	"github.com/ragent/router/internal/proxy"
)

// Rule defines a routing rule that matches requests to providers.
type Rule struct {
	// Name is a human-readable label for this rule.
	Name string `json:"name"`

	// Keywords that trigger this rule (case-insensitive match against prompt).
	Keywords []string `json:"keywords"`

	// Provider is the name of the target provider.
	Provider string `json:"provider"`

	// Priority determines rule precedence (higher = evaluated first).
	Priority int `json:"priority"`

	// MinTokens triggers this rule only if the prompt exceeds this length
	// (approximate token count = characters / 4). 0 = no minimum.
	MinTokens int `json:"min_tokens,omitempty"`
}

// RuleEngine matches requests to providers using keyword-based rules.
// Rules are evaluated in priority order; the first match wins.
// If no rule matches, the default provider is returned.
type RuleEngine struct {
	rules           []Rule
	providers       map[string]*proxy.ProviderConfig
	defaultProvider string
}

// NewRuleEngine creates a rule engine with the given rules and provider registry.
func NewRuleEngine(rules []Rule, providers map[string]*proxy.ProviderConfig, defaultProvider string) *RuleEngine {
	// Sort rules by priority descending.
	sorted := make([]Rule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Priority > sorted[j].Priority
	})

	return &RuleEngine{
		rules:           sorted,
		providers:       providers,
		defaultProvider: defaultProvider,
	}
}

// Match selects a provider based on the user's prompt and requested model.
// Returns nil if no provider could be determined.
func (e *RuleEngine) Match(prompt string, model string) *proxy.ProviderConfig {
	promptLower := strings.ToLower(prompt)

	// Try each rule in priority order.
	for _, rule := range e.rules {
		if !e.ruleMatches(rule, promptLower) {
			continue
		}
		if prov, ok := e.providers[rule.Provider]; ok && prov.Enabled {
			return prov
		}
	}

	// If a specific model was requested, try to find a matching provider.
	if model != "" && model != "auto" {
		for _, prov := range e.providers {
			if prov.Enabled && strings.Contains(strings.ToLower(prov.Name), strings.ToLower(model)) {
				return prov
			}
		}
	}

	// Fall back to the default provider.
	if prov, ok := e.providers[e.defaultProvider]; ok && prov.Enabled {
		return prov
	}

	// Return any enabled provider.
	for _, prov := range e.providers {
		if prov.Enabled {
			return prov
		}
	}
	return nil
}

// ruleMatches checks if a rule matches the given prompt.
func (e *RuleEngine) ruleMatches(rule Rule, promptLower string) bool {
	// Check min tokens constraint.
	if rule.MinTokens > 0 {
		estimatedTokens := len(promptLower) / 4
		if estimatedTokens < rule.MinTokens {
			return false
		}
	}

	// Check keywords.
	if len(rule.Keywords) == 0 {
		return false
	}
	for _, kw := range rule.Keywords {
		if strings.Contains(promptLower, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// AddRule adds a rule dynamically and re-sorts by priority.
func (e *RuleEngine) AddRule(rule Rule) {
	e.rules = append(e.rules, rule)
	sort.Slice(e.rules, func(i, j int) bool {
		return e.rules[i].Priority > e.rules[j].Priority
	})
}

// RemoveRule removes a rule by name.
func (e *RuleEngine) RemoveRule(name string) {
	for i, r := range e.rules {
		if r.Name == name {
			e.rules = append(e.rules[:i], e.rules[i+1:]...)
			return
		}
	}
}

// Rules returns a copy of all rules.
func (e *RuleEngine) Rules() []Rule {
	result := make([]Rule, len(e.rules))
	copy(result, e.rules)
	return result
}

// DefaultRules returns a sensible set of routing rules for AI coding assistants.
func DefaultRules() []Rule {
	return []Rule{
		{
			Name:     "Architecture & Design",
			Keywords: []string{"architecture", "design", "refactor", "架构", "设计", "重构", "system design", "distributed", "microservice"},
			Provider: "claude",
			Priority: 100,
		},
		{
			Name:     "Bug Fix & Debugging",
			Keywords: []string{"bug", "fix", "debug", "error", "修复", "调试", "issue", "crash", "exception"},
			Provider: "claude",
			Priority: 90,
		},
		{
			Name:     "Code Generation",
			Keywords: []string{"generate", "create", "implement", "生成", "创建", "implement", "write code", "build a"},
			Provider: "claude",
			Priority: 80,
		},
		{
			Name:     "Complex Analysis",
			Keywords: []string{"analyze", "analysis", "review", "explain", "分析", "审查"},
			Provider: "claude",
			Priority: 70,
			MinTokens: 300,
		},
		{
			Name:     "Simple Questions",
			Keywords: []string{"explain", "what is", "how to", "解释", "什么是", "how does", "why is"},
			Provider: "deepseek",
			Priority: 50,
		},
		{
			Name:     "Documentation",
			Keywords: []string{"document", "readme", "doc", "comment", "文档", "README", "documentation"},
			Provider: "deepseek",
			Priority: 40,
		},
	}
}
