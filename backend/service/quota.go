// Package service 提供业务逻辑层。
package service

import (
	"github.com/ragent/router/model"
)

// PreConsumeQuota 预扣配额（在请求发送到上游之前）。
func PreConsumeQuota(userId, tokenId, estimatedQuota int) error {
	// 检查用户配额
	user, err := model.GetUserById(userId)
	if err != nil {
		return err
	}

	if user.Quota < estimatedQuota {
		return ErrQuotaExceeded
	}

	// 扣减用户配额
	if err := model.DecreaseUserQuota(userId, estimatedQuota); err != nil {
		return err
	}

	// 扣减 Token 配额
	if tokenId > 0 {
		model.DecreaseTokenQuota(tokenId, estimatedQuota)
	}

	return nil
}

// PostConsumeQuota 结算配额（在请求完成后，使用实际用量）。
func PostConsumeQuota(userId, tokenId, preConsumed, actualQuota int) error {
	// 如果实际用量小于预扣，退还差额
	if actualQuota < preConsumed {
		refund := preConsumed - actualQuota
		model.IncreaseUserQuota(userId, refund)
		if tokenId > 0 {
			// Token 配额差额退还需要特殊处理
			model.DB.Model(&model.Token{}).Where("id = ?", tokenId).
				UpdateColumn("remain_quota", model.DB.Raw("remain_quota + ?", refund))
		}
	}

	// 增加已用配额
	model.IncreaseUsedQuota(userId, actualQuota)

	// 增加用户请求计数
	model.DB.Model(&model.User{}).Where("id = ?", userId).
		UpdateColumn("request_count", model.DB.Raw("request_count + 1"))

	return nil
}

// EstimateQuota 估算请求配额（基于 token 数量）。
func EstimateQuota(promptTokens, maxOutputTokens int) int {
	// 简单估算：input + estimated output
	return promptTokens + maxOutputTokens
}

// CalculateQuota 计算实际配额消耗。
func CalculateQuota(promptTokens, completionTokens int) int {
	return promptTokens + completionTokens
}
