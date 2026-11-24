// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package webx

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
)

var pinPattern = regexp.MustCompile(`^[0-9]{6}$`)

// SetPINFile 写入敏感操作二次验证使用的 6 位数字 PIN（只保存哈希）。
func SetPINFile(path, pin string) error {
	if !pinPattern.MatchString(pin) {
		return fmt.Errorf("PIN 必须为 6 位数字")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	key, err := pbkdf2.Key(sha256.New, pin, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	rec := passwordFile{
		User: "pin",
		Algo: passwordAlgo,
		Iter: pbkdf2Iterations,
		Salt: base64.StdEncoding.EncodeToString(salt),
		Hash: base64.StdEncoding.EncodeToString(key),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(path, data, 0o600)
}

// VerifyPINFile 校验 PIN。文件不存在时返回 os.ErrNotExist。
func VerifyPINFile(path, pin string) (bool, error) {
	rec, err := readPasswordFile(path)
	if err != nil {
		return false, err
	}
	if rec.User != "pin" || rec.Algo != passwordAlgo {
		return false, fmt.Errorf("PIN 文件格式不正确")
	}
	if !pinPattern.MatchString(pin) {
		return false, nil
	}
	salt, err := base64.StdEncoding.DecodeString(rec.Salt)
	if err != nil {
		return false, err
	}
	want, err := base64.StdEncoding.DecodeString(rec.Hash)
	if err != nil {
		return false, err
	}
	iter := rec.Iter
	if iter <= 0 {
		iter = pbkdf2Iterations
	}
	got, err := pbkdf2.Key(sha256.New, pin, salt, iter, len(want))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// PINConfigured 判断 PIN 文件是否已存在。
func PINConfigured(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
