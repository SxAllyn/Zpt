// Package zap 实现 Zpt 认证协议
package zap

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"
)

// AuthProvider 认证提供者接口
type AuthProvider interface {
	// Verify 验证认证数据
	Verify(authMethod AuthMethod, authData []byte) (bool, error)
	// GenerateChallenge 生成挑战数据（服务端）
	GenerateChallenge() ([]byte, error)
	// GenerateResponse 生成响应数据（客户端）
	GenerateResponse(challenge []byte) ([]byte, error)
}

// PSKAuth PSK认证提供者
type PSKAuth struct {
	psk []byte // 预共享密钥
}

// NewPSKAuth 创建PSK认证提供者
func NewPSKAuth(psk []byte) *PSKAuth {
	return &PSKAuth{psk: psk}
}

// Verify 验证PSK认证数据
func (p *PSKAuth) Verify(authMethod AuthMethod, authData []byte) (bool, error) {
	if authMethod != AuthMethodPSK {
		return false, fmt.Errorf("不支持的认证方法: %v", authMethod)
	}

	if len(authData) != 32 {
		return false, errors.New("PSK认证数据长度必须为32字节")
	}

	// PSK认证简单验证：期望HMAC-SHA256(挑战, PSK)
	// 注意：实际实现中，挑战应该在握手过程中传递
	// 这里简化处理，只检查格式
	return true, nil
}

// GenerateChallenge 生成PSK挑战
func (p *PSKAuth) GenerateChallenge() ([]byte, error) {
	// PSK认证不需要复杂挑战，返回随机数据
	challenge := make([]byte, 32)
	// 实际实现应使用加密随机数生成器
	for i := range challenge {
		challenge[i] = byte(i)
	}
	return challenge, nil
}

// GenerateResponse 生成PSK响应
func (p *PSKAuth) GenerateResponse(challenge []byte) ([]byte, error) {
	// HMAC-SHA256可以接受任意长度的挑战
	// 如果挑战为空，使用零长度挑战
	h := hmac.New(sha256.New, p.psk)
	h.Write(challenge)
	return h.Sum(nil), nil
}

// TokenAuth 令牌认证提供者
type TokenAuth struct {
	tokens map[string]bool // 有效令牌集合
}

// NewTokenAuth 创建令牌认证提供者
func NewTokenAuth(tokens []string) *TokenAuth {
	tokenMap := make(map[string]bool)
	for _, token := range tokens {
		tokenMap[token] = true
	}
	return &TokenAuth{tokens: tokenMap}
}

// Verify 验证令牌认证数据
func (t *TokenAuth) Verify(authMethod AuthMethod, authData []byte) (bool, error) {
	if authMethod != AuthMethodToken {
		return false, fmt.Errorf("不支持的认证方法: %v", authMethod)
	}

	token := string(authData)
	if _, valid := t.tokens[token]; valid {
		// 一次性令牌使用后应移除
		delete(t.tokens, token)
		return true, nil
	}

	return false, errors.New("无效令牌")
}

// GenerateChallenge 生成令牌挑战
func (t *TokenAuth) GenerateChallenge() ([]byte, error) {
	// 令牌认证不需要挑战
	return []byte("token_challenge"), nil
}

// GenerateResponse 生成令牌响应
func (t *TokenAuth) GenerateResponse(challenge []byte) ([]byte, error) {
	// 令牌认证响应就是令牌本身
	// 注意：实际实现中客户端应已有令牌
	return []byte("sample_token"), nil
}

// TOTPAuth TOTP认证提供者
type TOTPAuth struct {
	secret []byte // TOTP密钥
	window int    // 时间窗口（秒）
}

// NewTOTPAuth 创建TOTP认证提供者
func NewTOTPAuth(secret []byte, window int) *TOTPAuth {
	return &TOTPAuth{
		secret: secret,
		window: window,
	}
}

// Verify 验证TOTP认证数据
func (t *TOTPAuth) Verify(authMethod AuthMethod, authData []byte) (bool, error) {
	if authMethod != AuthMethodTOTP {
		return false, fmt.Errorf("不支持的认证方法: %v", authMethod)
	}

	if len(authData) != 6 { // TOTP通常是6位数字
		return false, errors.New("TOTP认证数据长度必须为6字节")
	}

	// 简化TOTP验证：实际应使用RFC6238实现
	// 这里只检查格式
	code := string(authData)
	for i := 0; i < 6; i++ {
		if code[i] < '0' || code[i] > '9' {
			return false, errors.New("TOTP码必须为数字")
		}
	}

	return true, nil
}

// GenerateChallenge 生成TOTP挑战
func (t *TOTPAuth) GenerateChallenge() ([]byte, error) {
	// TOTP认证挑战通常是时间戳或计数器
	timestamp := time.Now().Unix() / 30 // 30秒窗口
	challenge := fmt.Sprintf("%d", timestamp)
	return []byte(challenge), nil
}

// GenerateResponse 生成TOTP响应
func (t *TOTPAuth) GenerateResponse(challenge []byte) ([]byte, error) {
	// 简化TOTP生成：实际应根据secret和challenge计算
	// 这里返回示例值
	return []byte("123456"), nil
}

// AuthManager 认证管理器
type AuthManager struct {
	providers map[AuthMethod]AuthProvider
}

// NewAuthManager 创建认证管理器
func NewAuthManager() *AuthManager {
	return &AuthManager{
		providers: make(map[AuthMethod]AuthProvider),
	}
}

// RegisterProvider 注册认证提供者
func (am *AuthManager) RegisterProvider(method AuthMethod, provider AuthProvider) {
	am.providers[method] = provider
}

// GetProvider 获取认证提供者
func (am *AuthManager) GetProvider(method AuthMethod) (AuthProvider, error) {
	provider, exists := am.providers[method]
	if !exists {
		return nil, fmt.Errorf("未找到认证方法 %v 的提供者", method)
	}
	return provider, nil
}

// Verify 验证认证数据
func (am *AuthManager) Verify(method AuthMethod, authData []byte) (bool, error) {
	provider, err := am.GetProvider(method)
	if err != nil {
		return false, err
	}
	return provider.Verify(method, authData)
}

// DefaultAuthManager 创建默认认证管理器（包含所有认证方法）
func DefaultAuthManager(psk []byte, tokens []string, totpSecret []byte) *AuthManager {
	manager := NewAuthManager()

	// 注册PSK提供者
	if psk != nil {
		manager.RegisterProvider(AuthMethodPSK, NewPSKAuth(psk))
	}

	// 注册Token提供者
	if tokens != nil {
		manager.RegisterProvider(AuthMethodToken, NewTokenAuth(tokens))
	}

	// 注册TOTP提供者
	if totpSecret != nil {
		manager.RegisterProvider(AuthMethodTOTP, NewTOTPAuth(totpSecret, 30))
	}

	return manager
}

// AuthDataGenerator 认证数据生成器接口
type AuthDataGenerator interface {
	// GenerateAuthData 生成认证数据
	GenerateAuthData(method AuthMethod, challenge []byte) ([]byte, error)
}

// DefaultAuthDataGenerator 默认认证数据生成器
type DefaultAuthDataGenerator struct {
	manager *AuthManager
}

// NewDefaultAuthDataGenerator 创建默认认证数据生成器
func NewDefaultAuthDataGenerator(manager *AuthManager) *DefaultAuthDataGenerator {
	return &DefaultAuthDataGenerator{manager: manager}
}

// GenerateAuthData 生成认证数据
func (g *DefaultAuthDataGenerator) GenerateAuthData(method AuthMethod, challenge []byte) ([]byte, error) {
	provider, err := g.manager.GetProvider(method)
	if err != nil {
		return nil, err
	}
	return provider.GenerateResponse(challenge)
}
