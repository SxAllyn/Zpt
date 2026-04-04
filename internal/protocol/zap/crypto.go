// Package zap 实现 Zpt 认证协议
package zap

import (
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
)

// CryptoProvider 密码学提供者
type CryptoProvider struct {
	privateKey *ecdh.PrivateKey
	rand       io.Reader
}

// NewCryptoProvider 创建密码学提供者
func NewCryptoProvider(privateKey *ecdh.PrivateKey) *CryptoProvider {
	return &CryptoProvider{
		privateKey: privateKey,
		rand:       rand.Reader,
	}
}

// GenerateKeyPair 生成ECDH密钥对
func GenerateKeyPair() (*ecdh.PrivateKey, *ecdh.PublicKey, error) {
	curve := ecdh.P256()
	privateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("生成私钥失败: %w", err)
	}

	publicKey := privateKey.PublicKey()
	return privateKey, publicKey, nil
}

// PublicKeyBytes 获取公钥字节
func (cp *CryptoProvider) PublicKeyBytes() ([]byte, error) {
	if cp.privateKey == nil {
		return nil, errors.New("私钥未设置")
	}
	return cp.privateKey.PublicKey().Bytes(), nil
}

// DeriveSharedSecret 派生共享密钥（ECDH）
func (cp *CryptoProvider) DeriveSharedSecret(peerPublicKeyBytes []byte) ([]byte, error) {
	if cp.privateKey == nil {
		return nil, errors.New("私钥未设置")
	}

	// 解码对端公钥
	curve := ecdh.P256()
	peerPublicKey, err := curve.NewPublicKey(peerPublicKeyBytes)
	if err != nil {
		return nil, fmt.Errorf("解码对端公钥失败: %w", err)
	}

	// 计算共享密钥
	sharedSecret, err := cp.privateKey.ECDH(peerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("计算ECDH共享密钥失败: %w", err)
	}

	return sharedSecret, nil
}

// DeriveSessionKey 派生会话密钥（HKDF-SHA256）
// 根据规划：sessionKey = HKDF-SHA256(
//
//	salt: ClientRandom || ServerRandom,
//	ikm: ECDH(ClientPrivateKey, ServerPublicKey),
//	info: "zpt-session-key"
//
// )
func (cp *CryptoProvider) DeriveSessionKey(clientRandom, serverRandom, sharedSecret []byte) ([]byte, error) {
	if len(clientRandom) != 32 {
		return nil, errors.New("客户端随机数必须为32字节")
	}
	if len(serverRandom) != 32 {
		return nil, errors.New("服务端随机数必须为32字节")
	}
	if len(sharedSecret) == 0 {
		return nil, errors.New("共享密钥不能为空")
	}

	// 构建salt: ClientRandom || ServerRandom
	salt := make([]byte, 64)
	copy(salt[0:32], clientRandom)
	copy(salt[32:64], serverRandom)

	// HKDF参数
	info := []byte("zpt-session-key")

	// 派生密钥（32字节，AES-256）
	sessionKey, err := simpleHKDF(sharedSecret, salt, info, 32)
	if err != nil {
		return nil, fmt.Errorf("HKDF派生密钥失败: %w", err)
	}

	return sessionKey, nil
}

// SignData 签名数据
func (cp *CryptoProvider) SignData(data []byte) ([]byte, error) {
	if cp.privateKey == nil {
		return nil, errors.New("私钥未设置")
	}

	// 注意：ecdh.PrivateKey不支持签名，需要转换为ecdsa.PrivateKey
	// 这里简化处理，实际实现可能需要不同的密钥类型
	// 返回HMAC作为简化签名
	key := cp.privateKey.Bytes()
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil), nil
}

// VerifySignature 验证签名
func (cp *CryptoProvider) VerifySignature(publicKeyBytes, data, signature []byte) (bool, error) {
	// 简化验证：使用HMAC
	// 实际实现应使用ECDSA验证
	h := hmac.New(sha256.New, publicKeyBytes)
	h.Write(data)
	expectedMAC := h.Sum(nil)

	return hmac.Equal(signature, expectedMAC), nil
}

// GenerateRandom 生成随机数
func GenerateRandom(size int) ([]byte, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		return nil, fmt.Errorf("生成随机数失败: %w", err)
	}
	return data, nil
}

// ComputeHMAC 计算HMAC-SHA256
func ComputeHMAC(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// VerifyHMAC 验证HMAC
func VerifyHMAC(key, data, expectedMAC []byte) bool {
	actualMAC := ComputeHMAC(key, data)
	return hmac.Equal(actualMAC, expectedMAC)
}

// HashSHA256 计算SHA256哈希
func HashSHA256(data []byte) []byte {
	hash := sha256.Sum256(data)
	return hash[:]
}

// KeyExchange 密钥交换管理器
type KeyExchange struct {
	privateKey    *ecdh.PrivateKey
	publicKey     *ecdh.PublicKey
	peerPublicKey *ecdh.PublicKey
	sharedSecret  []byte
}

// NewKeyExchange 创建密钥交换管理器
func NewKeyExchange() (*KeyExchange, error) {
	privateKey, publicKey, err := GenerateKeyPair()
	if err != nil {
		return nil, err
	}

	return &KeyExchange{
		privateKey: privateKey,
		publicKey:  publicKey,
	}, nil
}

// NewKeyExchangeWithKey 使用现有密钥创建密钥交换管理器
func NewKeyExchangeWithKey(privateKey *ecdh.PrivateKey) *KeyExchange {
	return &KeyExchange{
		privateKey: privateKey,
		publicKey:  privateKey.PublicKey(),
	}
}

// PublicKeyBytes 返回公钥字节
func (ke *KeyExchange) PublicKeyBytes() []byte {
	return ke.publicKey.Bytes()
}

// SetPeerPublicKey 设置对端公钥
func (ke *KeyExchange) SetPeerPublicKey(publicKeyBytes []byte) error {
	curve := ecdh.P256()
	publicKey, err := curve.NewPublicKey(publicKeyBytes)
	if err != nil {
		return fmt.Errorf("解码对端公钥失败: %w", err)
	}

	ke.peerPublicKey = publicKey
	return nil
}

// ComputeSharedSecret 计算共享密钥
func (ke *KeyExchange) ComputeSharedSecret() ([]byte, error) {
	if ke.privateKey == nil {
		return nil, errors.New("私钥未设置")
	}
	if ke.peerPublicKey == nil {
		return nil, errors.New("对端公钥未设置")
	}

	sharedSecret, err := ke.privateKey.ECDH(ke.peerPublicKey)
	if err != nil {
		return nil, fmt.Errorf("计算共享密钥失败: %w", err)
	}

	ke.sharedSecret = sharedSecret
	return sharedSecret, nil
}

// GetSharedSecret 获取共享密钥
func (ke *KeyExchange) GetSharedSecret() []byte {
	return ke.sharedSecret
}

// SessionKeyManager 会话密钥管理器
type SessionKeyManager struct {
	clientRandom []byte
	serverRandom []byte
	sharedSecret []byte
	sessionKey   []byte
}

// NewSessionKeyManager 创建会话密钥管理器
func NewSessionKeyManager(clientRandom, serverRandom, sharedSecret []byte) *SessionKeyManager {
	return &SessionKeyManager{
		clientRandom: clientRandom,
		serverRandom: serverRandom,
		sharedSecret: sharedSecret,
	}
}

// Derive 派生会话密钥
func (sm *SessionKeyManager) Derive() ([]byte, error) {
	if sm.clientRandom == nil || sm.serverRandom == nil || sm.sharedSecret == nil {
		return nil, errors.New("缺少派生参数")
	}

	salt := make([]byte, len(sm.clientRandom)+len(sm.serverRandom))
	copy(salt[0:], sm.clientRandom)
	copy(salt[len(sm.clientRandom):], sm.serverRandom)

	info := []byte("zpt-session-key")
	sessionKey, err := simpleHKDF(sm.sharedSecret, salt, info, 32)
	if err != nil {
		return nil, fmt.Errorf("派生会话密钥失败: %w", err)
	}

	sm.sessionKey = sessionKey
	return sessionKey, nil
}

// GetSessionKey 获取会话密钥
func (sm *SessionKeyManager) GetSessionKey() []byte {
	return sm.sessionKey
}

// GenerateSessionID 生成会话ID
func GenerateSessionID() ([]byte, error) {
	return GenerateRandom(16)
}

// CryptoUtils 密码学工具函数
type CryptoUtils struct{}

// GenerateNonce 生成随机数（用于加密）
func (cu *CryptoUtils) GenerateNonce(size int) ([]byte, error) {
	return GenerateRandom(size)
}

// KDF 密钥派生函数
func (cu *CryptoUtils) KDF(secret, salt, info []byte, length int) ([]byte, error) {
	return simpleHKDF(secret, salt, info, length)
}

// Legacy support for older crypto APIs

// ecdsaKeyToECDH 将ECDSA密钥转换为ECDH密钥（简化接口）
func ecdsaKeyToECDH(ecdsaPriv *ecdsa.PrivateKey) (*ecdh.PrivateKey, error) {
	// 实际转换需要更复杂的处理
	// 这里返回错误，指示需要原生ECDH密钥
	return nil, errors.New("需要原生ECDH密钥，请使用GenerateKeyPair生成")
}

// isValidPublicKey 验证公钥是否有效
func isValidPublicKey(publicKeyBytes []byte) bool {
	if len(publicKeyBytes) == 0 {
		return false
	}

	// 简单检查：P-256公钥应为65字节（未压缩格式）
	// 或33字节（压缩格式）
	if len(publicKeyBytes) != 65 && len(publicKeyBytes) != 33 {
		return false
	}

	return true
}

// validateKeyLength 验证密钥长度
func validateKeyLength(key []byte, expected int) error {
	if len(key) != expected {
		return fmt.Errorf("密钥长度错误: 期望 %d, 实际 %d", expected, len(key))
	}
	return nil
}

// hkdfExtract HKDF提取函数
func hkdfExtract(hash func() hash.Hash, salt, ikm []byte) []byte {
	if len(salt) == 0 {
		salt = make([]byte, hash().Size())
	}
	h := hmac.New(hash, salt)
	h.Write(ikm)
	return h.Sum(nil)
}

// hkdfExpand HKDF扩展函数
func hkdfExpand(hash func() hash.Hash, prk []byte, info []byte, length int) ([]byte, error) {
	h := hash()
	if len(prk) < h.Size() {
		return nil, errors.New("prk长度不足")
	}

	// 计算需要多少轮
	n := (length + h.Size() - 1) / h.Size()
	if n > 255 {
		return nil, errors.New("输出长度过长")
	}

	var okm []byte
	var t []byte

	for i := 1; i <= n; i++ {
		mac := hmac.New(hash, prk)
		if t != nil {
			mac.Write(t)
		}
		mac.Write(info)
		mac.Write([]byte{byte(i)})
		t = mac.Sum(nil)
		okm = append(okm, t...)
	}

	return okm[:length], nil
}

// simpleHKDF 简化HKDF实现
func simpleHKDF(secret, salt, info []byte, length int) ([]byte, error) {
	// 提取阶段
	prk := hkdfExtract(sha256.New, salt, secret)

	// 扩展阶段
	return hkdfExpand(sha256.New, prk, info, length)
}
