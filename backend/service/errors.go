package service

import "errors"

var (
	ErrQuotaExceeded = errors.New("配额不足")
	ErrTokenInvalid  = errors.New("无效的 API Key")
	ErrTokenExpired  = errors.New("API Key 已过期")
	ErrTokenDisabled = errors.New("API Key 已被禁用")
	ErrUserDisabled  = errors.New("用户已被禁用")
	ErrChannelNone   = errors.New("没有可用的渠道")
)
