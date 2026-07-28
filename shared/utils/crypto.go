package utils

import (
	"crypto/rand"
	"encoding/hex"

	"golang.org/x/crypto/bcrypt"
)

// GenerateRandomKey 生成指定长度的随机 hex 字符串。
func GenerateRandomKey(length int) string {
	b := make([]byte, length/2+1)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)[:length]
}

// GenerateKey 生成 48 字符的 API Key。
func GenerateKey() string {
	return "sk-" + GenerateRandomKey(45)
}

// HashPassword 使用 bcrypt 对密码进行哈希。
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}

// CheckPasswordHash 验证密码与 bcrypt 哈希是否匹配。
func CheckPasswordHash(password, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
