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
	"errors"
	"fmt"
	"math/big"
	"time"
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

// ServerConfig 要求客户端出示证书，并在回调中与派生公钥比对。
func ServerConfig(token string) (*tls.Config, error) {
	cert, err := certificate(token)
	if err != nil {
		return nil, err
	}
	expected := Derive(token).Public().(ed25519.PublicKey)
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		ClientAuth:            tls.RequireAnyClientCert,
		VerifyPeerCertificate: verifyPeer(expected),
	}, nil
}

// ClientConfig 使用与 token 绑定的证书校验服务端身份。
// InsecureSkipVerify 只用于跳过系统根证书链校验（自签证书必然不在根证书中），
// 身份校验由 VerifyPeerCertificate 完成，不能删除。
func ClientConfig(token string) (*tls.Config, error) {
	cert, err := certificate(token)
	if err != nil {
		return nil, err
	}
	expected := Derive(token).Public().(ed25519.PublicKey)
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		ServerName:            "mtun",
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // 仅跳过链校验，见上方说明
		VerifyPeerCertificate: verifyPeer(expected),
	}, nil
}
