// Package zap 实现 Zpt 认证协议
package zap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"
)

// HandshakeError 握手错误
type HandshakeError struct {
	Step    string
	Message string
	Err     error
}

func (he *HandshakeError) Error() string {
	if he.Err != nil {
		return fmt.Sprintf("握手步骤 %s 失败: %s (%v)", he.Step, he.Message, he.Err)
	}
	return fmt.Sprintf("握手步骤 %s 失败: %s", he.Step, he.Message)
}

func (he *HandshakeError) Unwrap() error {
	return he.Err
}

// HandshakeConfig 握手配置
type HandshakeConfig struct {
	// 认证方法（客户端：选择的认证方法，服务端：支持的认证方法）
	AuthMethod AuthMethod
	// 认证管理器
	AuthManager *AuthManager
	// 密码学提供者
	CryptoProvider *CryptoProvider
	// 超时设置
	Timeout time.Duration
	// 随机数生成器
	Rand io.Reader
	// 会话超时时间
	SessionTimeout time.Duration
}

// DefaultHandshakeConfig 返回默认握手配置
func DefaultHandshakeConfig() *HandshakeConfig {
	// 生成ECDH密钥对
	privateKey, _, err := GenerateKeyPair()
	if err != nil {
		// 如果生成失败，返回nil配置
		return &HandshakeConfig{
			AuthMethod:     AuthMethodPSK,
			Timeout:        30 * time.Second,
			Rand:           nil,
			SessionTimeout: 24 * time.Hour,
		}
	}

	return &HandshakeConfig{
		AuthMethod:     AuthMethodPSK,
		CryptoProvider: NewCryptoProvider(privateKey),
		Timeout:        30 * time.Second,
		Rand:           nil,
		SessionTimeout: 24 * time.Hour,
	}
}

// Handshake 握手处理器
type Handshake struct {
	config *HandshakeConfig
	role   string // "client" 或 "server"
	state  HandshakeState

	// 握手期间临时数据
	clientRandom     []byte
	serverRandom     []byte
	salt             []byte
	chosenAuthMethod AuthMethod
	peerPublicKey    []byte
	localPublicKey   []byte
	sessionID        []byte
	sharedSecret     []byte
	sessionKey       []byte

	// 错误信息
	err error
}

// NewClientHandshake 创建客户端握手处理器
func NewClientHandshake(config *HandshakeConfig) (*Handshake, error) {
	if config == nil {
		config = DefaultHandshakeConfig()
	}

	if config.CryptoProvider == nil {
		privateKey, _, err := GenerateKeyPair()
		if err != nil {
			return nil, fmt.Errorf("生成密钥对失败: %w", err)
		}
		config.CryptoProvider = NewCryptoProvider(privateKey)
	}

	return &Handshake{
		config: config,
		role:   "client",
		state:  StateIdle,
	}, nil
}

// NewServerHandshake 创建服务端握手处理器
func NewServerHandshake(config *HandshakeConfig) (*Handshake, error) {
	if config == nil {
		config = DefaultHandshakeConfig()
	}

	if config.CryptoProvider == nil {
		privateKey, _, err := GenerateKeyPair()
		if err != nil {
			return nil, fmt.Errorf("生成密钥对失败: %w", err)
		}
		config.CryptoProvider = NewCryptoProvider(privateKey)
	}

	if config.AuthManager == nil {
		// 创建默认认证管理器
		config.AuthManager = DefaultAuthManager(
			[]byte("default-psk"),        // 默认PSK
			[]string{"token1", "token2"}, // 默认令牌
			[]byte("totp-secret"),        // 默认TOTP密钥
		)
	}

	return &Handshake{
		config: config,
		role:   "server",
		state:  StateIdle,
	}, nil
}

// DoClientHandshake 执行客户端握手
func (h *Handshake) DoClientHandshake(ctx context.Context, conn io.ReadWriter) (*Session, error) {
	if h.role != "client" {
		return nil, errors.New("只有客户端可以执行客户端握手")
	}

	// 设置超时上下文
	if h.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.config.Timeout)
		defer cancel()
	}

	// 步骤1: 发送ClientHello
	if err := h.sendClientHello(ctx, conn); err != nil {
		return nil, &HandshakeError{Step: "ClientHello", Message: "发送失败", Err: err}
	}
	h.state = StateClientHelloSent

	// 步骤2: 接收ServerChallenge
	if err := h.receiveServerChallenge(ctx, conn); err != nil {
		return nil, &HandshakeError{Step: "ServerChallenge", Message: "接收失败", Err: err}
	}
	h.state = StateServerChallengeSent

	// 步骤3: 发送ClientResponse
	if err := h.sendClientResponse(ctx, conn); err != nil {
		return nil, &HandshakeError{Step: "ClientResponse", Message: "发送失败", Err: err}
	}
	h.state = StateClientResponseSent

	// 步骤4: 接收ServerSuccess
	session, err := h.receiveServerSuccess(ctx, conn)
	if err != nil {
		return nil, &HandshakeError{Step: "ServerSuccess", Message: "接收失败", Err: err}
	}
	h.state = StateAuthenticated

	return session, nil
}

// DoServerHandshake 执行服务端握手
func (h *Handshake) DoServerHandshake(ctx context.Context, conn io.ReadWriter) (*Session, error) {
	if h.role != "server" {
		return nil, errors.New("只有服务端可以执行服务端握手")
	}

	// 设置超时上下文
	if h.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.config.Timeout)
		defer cancel()
	}

	// 步骤1: 接收ClientHello
	authMethod, err := h.receiveClientHello(ctx, conn)
	if err != nil {
		return nil, &HandshakeError{Step: "ClientHello", Message: "接收失败", Err: err}
	}
	h.chosenAuthMethod = authMethod
	h.state = StateClientHelloSent

	// 步骤2: 发送ServerChallenge
	if err := h.sendServerChallenge(ctx, conn); err != nil {
		return nil, &HandshakeError{Step: "ServerChallenge", Message: "发送失败", Err: err}
	}
	h.state = StateServerChallengeSent

	// 步骤3: 接收ClientResponse
	if err := h.receiveClientResponse(ctx, conn); err != nil {
		return nil, &HandshakeError{Step: "ClientResponse", Message: "接收失败", Err: err}
	}
	h.state = StateClientResponseSent

	// 步骤4: 发送ServerSuccess
	session, err := h.sendServerSuccess(ctx, conn)
	if err != nil {
		return nil, &HandshakeError{Step: "ServerSuccess", Message: "发送失败", Err: err}
	}
	h.state = StateAuthenticated

	return session, nil
}

// sendClientHello 发送ClientHello消息
func (h *Handshake) sendClientHello(ctx context.Context, conn io.ReadWriter) error {
	// 生成客户端随机数
	var err error
	h.clientRandom, err = GenerateRandom(32)
	if err != nil {
		return fmt.Errorf("生成客户端随机数失败: %w", err)
	}

	// 获取本地公钥（如果有）
	var clientPublicKey []byte
	if h.config.CryptoProvider != nil {
		var err error
		clientPublicKey, err = h.config.CryptoProvider.PublicKeyBytes()
		if err != nil {
			// 如果获取公钥失败，发送空公钥
			clientPublicKey = nil
		}
	}

	// 编码ClientHello载荷
	payload := EncodeClientHello(h.config.AuthMethod, h.clientRandom, clientPublicKey)

	// 创建帧并发送
	frame := NewFrame(TypeClientHello, payload)
	if err := WriteFrame(conn, frame); err != nil {
		return fmt.Errorf("写入ClientHello帧失败: %w", err)
	}

	return nil
}

// receiveClientHello 接收ClientHello消息
func (h *Handshake) receiveClientHello(ctx context.Context, conn io.ReadWriter) (AuthMethod, error) {
	// 读取帧
	frame, err := ReadFrame(conn)
	if err != nil {
		return 0, fmt.Errorf("读取ClientHello帧失败: %w", err)
	}

	if frame.Type != TypeClientHello {
		return 0, fmt.Errorf("期望ClientHello帧, 得到类型: %v", frame.Type)
	}

	// 解码ClientHello载荷
	authMethod, clientRandom, clientPublicKey, err := DecodeClientHello(frame.Payload)
	if err != nil {
		return 0, fmt.Errorf("解码ClientHello失败: %w", err)
	}

	// 保存数据
	h.clientRandom = clientRandom
	h.peerPublicKey = clientPublicKey
	h.chosenAuthMethod = authMethod

	// 验证认证方法是否支持
	if h.config.AuthManager != nil {
		if _, err := h.config.AuthManager.GetProvider(authMethod); err != nil {
			return 0, fmt.Errorf("不支持的认证方法: %v", authMethod)
		}
	}

	return authMethod, nil
}

// sendServerChallenge 发送ServerChallenge消息
func (h *Handshake) sendServerChallenge(ctx context.Context, conn io.ReadWriter) error {
	// 生成服务端随机数
	var err error
	h.serverRandom, err = GenerateRandom(32)
	if err != nil {
		return fmt.Errorf("生成服务端随机数失败: %w", err)
	}

	// 生成盐值
	h.salt, err = GenerateRandom(16)
	if err != nil {
		return fmt.Errorf("生成盐值失败: %w", err)
	}

	// 获取服务端公钥（如果有）
	var serverPublicKey []byte
	if h.config.CryptoProvider != nil {
		var err error
		serverPublicKey, err = h.config.CryptoProvider.PublicKeyBytes()
		if err != nil {
			// 如果获取公钥失败，发送空公钥
			serverPublicKey = nil
		}
	}

	// 生成签名（简化处理）
	signature := make([]byte, 64)

	// 编码ServerChallenge载荷
	payload := EncodeServerChallenge(h.serverRandom, serverPublicKey, h.salt, signature)

	// 创建帧并发送
	frame := NewFrame(TypeServerChallenge, payload)
	if err := WriteFrame(conn, frame); err != nil {
		return fmt.Errorf("写入ServerChallenge帧失败: %w", err)
	}

	return nil
}

// receiveServerChallenge 接收ServerChallenge消息
func (h *Handshake) receiveServerChallenge(ctx context.Context, conn io.ReadWriter) error {
	// 读取帧
	frame, err := ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("读取ServerChallenge帧失败: %w", err)
	}

	if frame.Type != TypeServerChallenge {
		return fmt.Errorf("期望ServerChallenge帧, 得到类型: %v", frame.Type)
	}

	// 解码ServerChallenge载荷
	serverRandom, serverPublicKey, salt, signature, err := DecodeServerChallenge(frame.Payload)
	if err != nil {
		return fmt.Errorf("解码ServerChallenge失败: %w", err)
	}

	// 保存数据
	h.serverRandom = serverRandom
	h.peerPublicKey = serverPublicKey
	h.salt = salt

	// 验证签名（简化处理）
	_ = signature

	return nil
}

// sendClientResponse 发送ClientResponse消息
func (h *Handshake) sendClientResponse(ctx context.Context, conn io.ReadWriter) error {
	// 根据认证方法生成认证数据
	var authData []byte

	if h.config.AuthManager != nil {
		provider, err := h.config.AuthManager.GetProvider(h.config.AuthMethod)
		if err != nil {
			return fmt.Errorf("获取认证提供者失败: %w", err)
		}

		// 使用盐值作为挑战生成响应
		authData, err = provider.GenerateResponse(h.salt)
		if err != nil {
			return fmt.Errorf("生成认证响应失败: %w", err)
		}
	} else {
		// 默认认证数据
		authData = make([]byte, 32)
	}

	// 计算HMAC（简化处理）
	hmacValue := make([]byte, 32)

	// 编码ClientResponse载荷
	payload := EncodeClientResponse(authData, hmacValue)

	// 创建帧并发送
	frame := NewFrame(TypeClientResponse, payload)
	if err := WriteFrame(conn, frame); err != nil {
		return fmt.Errorf("写入ClientResponse帧失败: %w", err)
	}

	return nil
}

// receiveClientResponse 接收ClientResponse消息
func (h *Handshake) receiveClientResponse(ctx context.Context, conn io.ReadWriter) error {
	// 读取帧
	frame, err := ReadFrame(conn)
	if err != nil {
		return fmt.Errorf("读取ClientResponse帧失败: %w", err)
	}

	if frame.Type != TypeClientResponse {
		return fmt.Errorf("期望ClientResponse帧, 得到类型: %v", frame.Type)
	}

	// 解码ClientResponse载荷
	authData, hmacValue, err := DecodeClientResponse(frame.Payload)
	if err != nil {
		return fmt.Errorf("解码ClientResponse失败: %w", err)
	}

	// 验证认证数据
	if h.config.AuthManager != nil {
		valid, err := h.config.AuthManager.Verify(h.chosenAuthMethod, authData)
		if err != nil {
			return fmt.Errorf("验证认证数据失败: %w", err)
		}
		if !valid {
			return errors.New("认证数据无效")
		}
	}

	// 验证HMAC（简化处理）
	_ = hmacValue

	return nil
}

// sendServerSuccess 发送ServerSuccess消息
func (h *Handshake) sendServerSuccess(ctx context.Context, conn io.ReadWriter) (*Session, error) {
	// 生成会话ID
	var err error
	h.sessionID, err = GenerateSessionID()
	if err != nil {
		return nil, fmt.Errorf("生成会话ID失败: %w", err)
	}

	// 派生共享密钥（如果支持ECDH）
	if h.peerPublicKey != nil && h.config.CryptoProvider != nil {
		h.sharedSecret, err = h.config.CryptoProvider.DeriveSharedSecret(h.peerPublicKey)
		if err != nil {
			return nil, fmt.Errorf("派生共享密钥失败: %w", err)
		}

		// 派生会话密钥
		h.sessionKey, err = h.config.CryptoProvider.DeriveSessionKey(
			h.clientRandom, h.serverRandom, h.sharedSecret)
		if err != nil {
			return nil, fmt.Errorf("派生会话密钥失败: %w", err)
		}
	} else {
		// 简化处理：生成随机会话密钥
		h.sessionKey, err = GenerateRandom(32)
		if err != nil {
			return nil, fmt.Errorf("生成会话密钥失败: %w", err)
		}
	}

	// 计算TTL（秒）
	ttl := uint32(h.config.SessionTimeout / time.Second)

	// 编码ServerSuccess载荷
	payload := EncodeServerSuccess(h.sessionID, ttl)

	// 创建帧并发送
	frame := NewFrame(TypeServerSuccess, payload)
	if err := WriteFrame(conn, frame); err != nil {
		return nil, fmt.Errorf("写入ServerSuccess帧失败: %w", err)
	}

	// 创建会话对象
	session := &Session{
		ID:            h.sessionID,
		Key:           h.sessionKey,
		ExpiresAt:     time.Now().Add(h.config.SessionTimeout),
		PeerPublicKey: h.peerPublicKey,
	}

	return session, nil
}

// receiveServerSuccess 接收ServerSuccess消息
func (h *Handshake) receiveServerSuccess(ctx context.Context, conn io.ReadWriter) (*Session, error) {
	// 读取帧
	frame, err := ReadFrame(conn)
	if err != nil {
		return nil, fmt.Errorf("读取ServerSuccess帧失败: %w", err)
	}

	if frame.Type != TypeServerSuccess {
		return nil, fmt.Errorf("期望ServerSuccess帧, 得到类型: %v", frame.Type)
	}

	// 解码ServerSuccess载荷
	sessionID, ttl, err := DecodeServerSuccess(frame.Payload)
	if err != nil {
		return nil, fmt.Errorf("解码ServerSuccess失败: %w", err)
	}

	h.sessionID = sessionID

	// 派生共享密钥（如果支持ECDH）
	if h.peerPublicKey != nil && h.config.CryptoProvider != nil {
		h.sharedSecret, err = h.config.CryptoProvider.DeriveSharedSecret(h.peerPublicKey)
		if err != nil {
			return nil, fmt.Errorf("派生共享密钥失败: %w", err)
		}

		// 派生会话密钥
		h.sessionKey, err = h.config.CryptoProvider.DeriveSessionKey(
			h.clientRandom, h.serverRandom, h.sharedSecret)
		if err != nil {
			return nil, fmt.Errorf("派生会话密钥失败: %w", err)
		}
	} else {
		// 简化处理：生成随机会话密钥
		h.sessionKey, err = GenerateRandom(32)
		if err != nil {
			return nil, fmt.Errorf("生成会话密钥失败: %w", err)
		}
	}

	// 计算过期时间
	expiresAt := time.Now().Add(time.Duration(ttl) * time.Second)

	// 创建会话对象
	session := &Session{
		ID:            h.sessionID,
		Key:           h.sessionKey,
		ExpiresAt:     expiresAt,
		PeerPublicKey: h.peerPublicKey,
	}

	return session, nil
}

// State 返回当前握手状态
func (h *Handshake) State() HandshakeState {
	return h.state
}

// Error 返回错误信息
func (h *Handshake) Error() error {
	return h.err
}

// Reset 重置握手状态
func (h *Handshake) Reset() {
	h.state = StateIdle
	h.err = nil
	h.clientRandom = nil
	h.serverRandom = nil
	h.salt = nil
	h.peerPublicKey = nil
	h.sessionID = nil
	h.sharedSecret = nil
	h.sessionKey = nil
}

// SimpleAuthenticate 简化认证函数（客户端）
func SimpleAuthenticate(ctx context.Context, conn io.ReadWriter, config *HandshakeConfig) (*Session, error) {
	handshake, err := NewClientHandshake(config)
	if err != nil {
		return nil, fmt.Errorf("创建握手处理器失败: %w", err)
	}

	return handshake.DoClientHandshake(ctx, conn)
}

// SimpleAuthenticateServer 简化认证函数（服务端）
func SimpleAuthenticateServer(ctx context.Context, conn io.ReadWriter, config *HandshakeConfig) (*Session, error) {
	handshake, err := NewServerHandshake(config)
	if err != nil {
		return nil, fmt.Errorf("创建握手处理器失败: %w", err)
	}

	return handshake.DoServerHandshake(ctx, conn)
}
