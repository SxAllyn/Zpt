// Package zap 实现 Zpt 认证协议
// Zap (Zpt Authentication Protocol) 提供端到端认证和密钥交换功能
package zap

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"time"
)

const (
	// ProtocolMagic 协议魔数
	ProtocolMagic uint32 = 0x5A415020 // "ZAP "
	// ProtocolVersion 协议版本
	ProtocolVersion uint8 = 0x01
)

// AuthMethod 认证方法枚举
type AuthMethod uint8

const (
	// AuthMethodPSK 预共享密钥认证
	AuthMethodPSK AuthMethod = 0x01
	// AuthMethodToken 一次性令牌认证
	AuthMethodToken AuthMethod = 0x02
	// AuthMethodTOTP 基于时间的一次性密码认证
	AuthMethodTOTP AuthMethod = 0x03
)

// Session 表示一个已认证的会话
type Session struct {
	// 会话标识
	ID []byte
	// 会话密钥（用于后续通信加密）
	Key []byte
	// 过期时间
	ExpiresAt time.Time
	// 对端公钥
	PeerPublicKey []byte
}

// Config Zap配置
type Config struct {
	// 支持的认证方法
	AuthMethods []AuthMethod
	// 预共享密钥（PSK模式）
	PSK []byte
	// ECDH私钥
	PrivateKey *ecdh.PrivateKey
	// 令牌验证器（Token/TOTP模式）
	TokenVerifier TokenVerifier
	// 会话超时时间
	SessionTimeout time.Duration
	// 随机数生成器
	Rand io.Reader
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	// 生成ECDH私钥
	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		// 如果生成失败，返回nil私钥的配置
		return &Config{
			AuthMethods:    []AuthMethod{AuthMethodPSK, AuthMethodToken, AuthMethodTOTP},
			SessionTimeout: 24 * time.Hour,
			Rand:           rand.Reader,
		}
	}

	return &Config{
		AuthMethods:    []AuthMethod{AuthMethodPSK, AuthMethodToken, AuthMethodTOTP},
		PrivateKey:     privateKey,
		SessionTimeout: 24 * time.Hour,
		Rand:           rand.Reader,
	}
}

// HandshakeState 握手状态
type HandshakeState uint8

const (
	// StateIdle 空闲状态
	StateIdle HandshakeState = iota
	// StateClientHelloSent 客户端已发送Hello
	StateClientHelloSent
	// StateServerChallengeSent 服务端已发送Challenge
	StateServerChallengeSent
	// StateClientResponseSent 客户端已发送Response
	StateClientResponseSent
	// StateAuthenticated 已认证
	StateAuthenticated
	// StateFailed 失败
	StateFailed
)

// convertConfig 将Config转换为HandshakeConfig
func convertConfig(config *Config) *HandshakeConfig {
	if config == nil {
		return DefaultHandshakeConfig()
	}

	handshakeConfig := &HandshakeConfig{
		AuthMethod:     AuthMethodPSK, // 默认使用PSK
		Timeout:        30 * time.Second,
		Rand:           config.Rand,
		SessionTimeout: config.SessionTimeout,
	}

	// 设置认证方法（选择第一个支持的）
	if len(config.AuthMethods) > 0 {
		handshakeConfig.AuthMethod = config.AuthMethods[0]
	}

	// 创建认证管理器
	if config.PSK != nil || config.TokenVerifier != nil {
		authManager := NewAuthManager()

		if config.PSK != nil {
			authManager.RegisterProvider(AuthMethodPSK, NewPSKAuth(config.PSK))
		}

		// 注意：Token和TOTP需要更复杂的集成
		// 这里简化处理
	}

	// 创建密码学提供者
	if config.PrivateKey != nil {
		handshakeConfig.CryptoProvider = NewCryptoProvider(config.PrivateKey)
	}

	return handshakeConfig
}

// Authenticate 执行完整的客户端认证流程
func Authenticate(ctx context.Context, conn io.ReadWriter, config *Config) (*Session, error) {
	handshakeConfig := convertConfig(config)
	return SimpleAuthenticate(ctx, conn, handshakeConfig)
}

// AuthenticateServer 执行服务端认证流程
func AuthenticateServer(ctx context.Context, conn io.ReadWriter, config *Config) (*Session, error) {
	handshakeConfig := convertConfig(config)
	return SimpleAuthenticateServer(ctx, conn, handshakeConfig)
}

// TokenVerifier 令牌验证器接口
type TokenVerifier interface {
	// VerifyToken 验证一次性令牌
	VerifyToken(token string) (bool, error)
	// VerifyTOTP 验证TOTP令牌
	VerifyTOTP(code string) (bool, error)
}

// AuthHandler 认证处理器接口
type AuthHandler interface {
	// Authenticate 验证客户端认证数据
	Authenticate(authMethod AuthMethod, authData []byte) (bool, error)
}
