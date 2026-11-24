// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

// Package secret 负责把密码转换成不可逆的本地存储哈希（seed）。
//
// 设计要点：
//   - 所有接入类密码（全局接入密码、节点独立接入密码）落盘时只保存 seed；
//   - seed 固定使用同一套盐与迭代次数，因此同一密码总是得到同一 seed，
//     服务端与客户端可以各自独立计算并比对；
//   - seed 本身作为 TLS 派生种子使用，不再需要在任何一方保存明文密码。
//
// 盐是固定值而非随机值：seed 需要可复算（客户端无法读取服务端的随机盐），
// 随机盐会让双方算出的 seed 不同，从而无法完成握手。
package secret

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
)

const (
	// formatPrefix 标识文件中的值已经是本地哈希而非明文。
	formatPrefix = "v1:"
	// salt 是固定盐，保证同一密码在各端得到相同 seed。
	salt = "mtun-local-secret-v1"
	// iterations 提高暴力破解成本；仅在设置密码时计算一次。
	iterations = 100000
	keyLen     = 32
)

// Seed 返回密码的本地存储哈希，形如 v1:<64 位 hex>。
func Seed(password string) string {
	key, err := pbkdf2.Key(sha256.New, password, []byte(salt), iterations, keyLen)
	if err != nil {
		// pbkdf2 只会在参数非法时失败，这里参数固定，兜底返回空串。
		return ""
	}
	return formatPrefix + hex.EncodeToString(key)
}

// IsSeed 判断字符串是否已经是本地存储哈希。
func IsSeed(s string) bool {
	return strings.HasPrefix(s, formatPrefix) && len(s) == len(formatPrefix)+keyLen*2
}

// Normalize 把明文或已有 seed 统一转换成 seed。
func Normalize(s string) string {
	if IsSeed(s) {
		return s
	}
	return Seed(s)
}

// Equal 以常量时间比较两个 seed。
func Equal(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
