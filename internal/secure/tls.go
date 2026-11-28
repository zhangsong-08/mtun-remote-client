// Copyright (c) 2026 song08
// SPDX-License-Identifier: MIT

package secure

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"mtun/internal/secret"
)

// derivePrefix 是密钥派生的域分隔前缀，改动会让新旧版本不兼容。
const derivePrefix = "mtun/1/tls|"

// Derive 从客户端接入密码派生 Ed25519 私钥。
// 服务端与客户端使用同一密钥，因此不需要分发任何证书文件；
// 密钥不一致时握手阶段的证书校验会直接失败。
func Derive(token string) ed25519.PrivateKey {
	sum := sha256.Sum256([]byte(derivePrefix + token))
	return ed25519.NewKeyFromSeed(sum[:])
}

func certificate(token string) (tls.Certificate, error) {
	priv := Derive(token)
	pub := priv.Public().(ed25519.PublicKey)

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "mtun"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("生成 mtun 自签证书: %w", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func verifyPeer(expected ed25519.PublicKey) func([][]byte, [][]*x509.Certificate) error {
	return func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
		if len(rawCerts) == 0 {
			return errors.New("mtun: 对端未提供证书")
		}
		cert, err := x509.ParseCertificate(rawCerts[0])
		if err != nil {
			return fmt.Errorf("mtun: 解析对端证书: %w", err)
		}
		pub, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok || !bytes.Equal(pub, expected) {
			return errors.New("mtun: 接入密码不匹配")
		}
		return nil
	}
}

// Seed 把明文密码转换成双方一致的本地哈希（用于派生证书）。
// 服务端只保存 seed，客户端在设置密码时即时计算，二者都不落盘明文。
func Seed(password string) string {
	return secret.Seed(password)
}

// requestPassword 是公开固定的「申请口令」，仅用于让没有接入密码的客户端
// 完成 TLS 握手并提交接入申请。它不授予任何节点权限：
// 服务端收到申请后必须由管理员在面板批准，才会下发该节点自己的密码。
const requestPassword = "mtun-join-request-v1"

var (
	requestSeedOnce sync.Once
	requestSeedVal  string
	requestSNIOnce  sync.Once
	requestSNIVal   string
)

// RequestSeed 返回申请通道使用的本地哈希，所有安装共用同一值。
func RequestSeed() string {
	requestSeedOnce.Do(func() {
		requestSeedVal = secret.Seed(requestPassword)
	})
	return requestSeedVal
}

// RequestFingerprint 返回申请通道的 SNI 标识。
func RequestFingerprint() string {
	requestSNIOnce.Do(func() {
		requestSNIVal = TokenFingerprint(RequestSeed())
	})
	return requestSNIVal
}

// ServerConfig 要求客户端出示证书，并在回调中与派生公钥比对。
// seed 是接入密码的本地哈希（secret.Seed 的产物），不是明文密码。
func ServerConfig(seed string) (*tls.Config, error) {
	cert, err := certificate(seed)
	if err != nil {
		return nil, err
	}
	expected := Derive(seed).Public().(ed25519.PublicKey)
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: verifyPeer(expected),
	}, nil
}

// TokenFingerprint 返回本地哈希对应的 SNI 标识。
// 客户端在 ClientHello 中携带它，服务端据此选择正确的口令证书，
// 从而支持「每个节点一套密码」：不必在握手中暴露密码或哈希本身。
func TokenFingerprint(seed string) string {
	sum := sha256.Sum256([]byte(derivePrefix + "sni|" + seed))
	return "mtun-" + hex.EncodeToString(sum[:8])
}

// ServerConfigFor 返回按客户端 SNI 动态选择口令的 TLS 配置。
// tokenFor 收到 SNI，返回应使用的口令；返回 false 表示拒绝握手。
// 常见的空 SNI 或旧客户端的 "mtun" 由调用方决定是否回退到默认口令。
func ServerConfigFor(tokenFor func(sni string) (string, bool)) (*tls.Config, error) {
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ClientAuth: tls.RequireAnyClientCert,
		GetConfigForClient: func(hello *tls.ClientHelloInfo) (*tls.Config, error) {
			token, ok := tokenFor(hello.ServerName)
			if !ok || token == "" {
				return nil, errors.New("mtun: 接入密码无效或不允许接入")
			}
			return ServerConfig(token)
		},
	}, nil
}

// ClientConfig 使用与 seed 绑定的证书校验服务端身份。
// InsecureSkipVerify 只用于跳过系统根证书链校验（自签证书必然不在根证书中），
// 身份校验由 VerifyPeerCertificate 完成，不能删除。
func ClientConfig(seed string) (*tls.Config, error) {
	cert, err := certificate(seed)
	if err != nil {
		return nil, err
	}
	expected := Derive(seed).Public().(ed25519.PublicKey)
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		// ServerName 用口令指纹而非固定 "mtun"，服务端据此匹配到该口令的证书，
		// 从而支持每个节点使用不同的接入密码。
		ServerName:            TokenFingerprint(seed),
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // 仅跳过链校验，见上方说明
		VerifyPeerCertificate: verifyPeer(expected),
	}, nil
}
