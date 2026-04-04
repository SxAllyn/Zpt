// Package zap 实现 Zpt 认证协议测试
package zap

import (
	"bytes"
	"context"
	"io"
	"testing"
)

// TestFrameEncodeDecode 测试帧编码解码
func TestFrameEncodeDecode(t *testing.T) {
	tests := []struct {
		name     string
		frame    *Frame
		wantType FrameType
		wantLen  int
	}{
		{
			name:     "ClientHello 帧",
			frame:    NewFrame(TypeClientHello, []byte("hello")),
			wantType: TypeClientHello,
			wantLen:  5,
		},
		{
			name:     "ServerChallenge 帧",
			frame:    NewFrame(TypeServerChallenge, []byte("challenge")),
			wantType: TypeServerChallenge,
			wantLen:  9,
		},
		{
			name:     "ClientResponse 帧",
			frame:    NewFrame(TypeClientResponse, make([]byte, 32)),
			wantType: TypeClientResponse,
			wantLen:  32,
		},
		{
			name:     "ServerSuccess 帧",
			frame:    NewFrame(TypeServerSuccess, []byte("success")),
			wantType: TypeServerSuccess,
			wantLen:  7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 编码帧
			var buf bytes.Buffer
			if err := WriteFrame(&buf, tt.frame); err != nil {
				t.Fatalf("WriteFrame 失败: %v", err)
			}

			// 解码帧
			decoded, err := ReadFrame(&buf)
			if err != nil {
				t.Fatalf("ReadFrame 失败: %v", err)
			}

			// 验证类型
			if decoded.Type != tt.wantType {
				t.Errorf("帧类型不匹配: 期望 %v, 实际 %v", tt.wantType, decoded.Type)
			}

			// 验证载荷长度
			if len(decoded.Payload) != tt.wantLen {
				t.Errorf("载荷长度不匹配: 期望 %d, 实际 %d", tt.wantLen, len(decoded.Payload))
			}

			// 验证魔数和版本
			if decoded.Magic != ProtocolMagic {
				t.Errorf("魔数不匹配: 期望 %x, 实际 %x", ProtocolMagic, decoded.Magic)
			}
			if decoded.Version != ProtocolVersion {
				t.Errorf("版本不匹配: 期望 %d, 实际 %d", ProtocolVersion, decoded.Version)
			}
		})
	}
}

// TestAuthProviders 测试认证提供者
func TestAuthProviders(t *testing.T) {
	// PSK 认证
	t.Run("PSKAuth", func(t *testing.T) {
		psk := []byte("test-psk-key-32-bytes-long-example!")
		provider := NewPSKAuth(psk)

		// 生成挑战
		challenge, err := provider.GenerateChallenge()
		if err != nil {
			t.Fatalf("生成挑战失败: %v", err)
		}

		// 生成响应
		response, err := provider.GenerateResponse(challenge)
		if err != nil {
			t.Fatalf("生成响应失败: %v", err)
		}

		// 验证响应格式
		if len(response) != 32 {
			t.Errorf("响应长度应为32字节, 实际 %d", len(response))
		}

		// 验证认证方法
		valid, err := provider.Verify(AuthMethodPSK, response)
		if err != nil {
			t.Errorf("验证失败: %v", err)
		}
		if !valid {
			t.Error("PSK 验证应成功")
		}

		// 测试错误的认证方法
		valid, err = provider.Verify(AuthMethodToken, response)
		if err == nil {
			t.Error("期望错误: 不支持的认证方法")
		}
	})

	// Token 认证
	t.Run("TokenAuth", func(t *testing.T) {
		tokens := []string{"token1", "token2", "token3"}
		provider := NewTokenAuth(tokens)

		// 验证有效令牌
		valid, err := provider.Verify(AuthMethodToken, []byte("token1"))
		if err != nil {
			t.Fatalf("验证失败: %v", err)
		}
		if !valid {
			t.Error("令牌验证应成功")
		}

		// 令牌应被移除（一次性使用）
		valid, err = provider.Verify(AuthMethodToken, []byte("token1"))
		if valid || err == nil {
			t.Error("令牌应已失效")
		}

		// 验证无效令牌
		valid, err = provider.Verify(AuthMethodToken, []byte("invalid-token"))
		if valid || err == nil {
			t.Error("无效令牌应失败")
		}
	})

	// TOTP 认证（简化版本）
	t.Run("TOTPAuth", func(t *testing.T) {
		secret := []byte("totp-secret-key")
		provider := NewTOTPAuth(secret, 30)

		// 生成挑战
		challenge, err := provider.GenerateChallenge()
		if err != nil {
			t.Fatalf("生成挑战失败: %v", err)
		}

		// 验证挑战格式
		if len(challenge) == 0 {
			t.Error("挑战不应为空")
		}

		// 生成响应
		response, err := provider.GenerateResponse(challenge)
		if err != nil {
			t.Fatalf("生成响应失败: %v", err)
		}

		// 简化验证（只检查格式）
		valid, err := provider.Verify(AuthMethodTOTP, response)
		if err != nil {
			t.Errorf("验证失败: %v", err)
		}
		if !valid {
			t.Error("TOTP 验证应成功（简化版本）")
		}
	})
}

// TestAuthManager 测试认证管理器
func TestAuthManager(t *testing.T) {
	manager := NewAuthManager()

	// 注册提供者
	psk := []byte("test-psk")
	manager.RegisterProvider(AuthMethodPSK, NewPSKAuth(psk))

	// 获取提供者
	provider, err := manager.GetProvider(AuthMethodPSK)
	if err != nil {
		t.Fatalf("获取提供者失败: %v", err)
	}
	if provider == nil {
		t.Fatal("提供者不应为nil")
	}

	// 验证未注册的认证方法
	_, err = manager.GetProvider(AuthMethodToken)
	if err == nil {
		t.Error("期望错误: 未找到认证方法")
	}

	// 验证认证数据
	challenge := make([]byte, 32)
	for i := range challenge {
		challenge[i] = byte(i)
	}
	response, err := provider.GenerateResponse(challenge)
	if err != nil {
		t.Fatalf("生成响应失败: %v", err)
	}

	valid, err := manager.Verify(AuthMethodPSK, response)
	if err != nil {
		t.Errorf("验证失败: %v", err)
	}
	if !valid {
		t.Error("验证应成功")
	}
}

// TestCryptoKeyDerivation 测试密钥派生
func TestCryptoKeyDerivation(t *testing.T) {
	// 生成密钥对
	privateKey, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成密钥对失败: %v", err)
	}

	// 创建密码学提供者
	provider := NewCryptoProvider(privateKey)

	// 模拟对端密钥对
	_, peerPublicKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("生成对端密钥对失败: %v", err)
	}

	// 派生共享密钥
	sharedSecret, err := provider.DeriveSharedSecret(peerPublicKey.Bytes())
	if err != nil {
		t.Fatalf("派生共享密钥失败: %v", err)
	}

	// 验证共享密钥长度（P-256的ECDH共享密钥为32字节）
	if len(sharedSecret) != 32 {
		t.Errorf("共享密钥长度应为32字节, 实际 %d", len(sharedSecret))
	}

	// 测试会话密钥派生
	clientRandom := make([]byte, 32)
	serverRandom := make([]byte, 32)
	for i := range clientRandom {
		clientRandom[i] = byte(i)
		serverRandom[i] = byte(i + 32)
	}

	sessionKey, err := provider.DeriveSessionKey(clientRandom, serverRandom, sharedSecret)
	if err != nil {
		t.Fatalf("派生会话密钥失败: %v", err)
	}

	if len(sessionKey) != 32 {
		t.Errorf("会话密钥长度应为32字节, 实际 %d", len(sessionKey))
	}

	// 验证相同输入产生相同输出
	sessionKey2, err := provider.DeriveSessionKey(clientRandom, serverRandom, sharedSecret)
	if err != nil {
		t.Fatalf("第二次派生会话密钥失败: %v", err)
	}

	if !bytes.Equal(sessionKey, sessionKey2) {
		t.Error("相同输入应产生相同的会话密钥")
	}
}

// TestHandshakeSimple 测试简化握手流程
func TestHandshakeSimple(t *testing.T) {
	// 创建内存连接（双向）
	clientConn, serverConn := newMemoryPipe()

	// 使用相同的PSK
	psk := []byte("test-psk-for-handshake-32-bytes-long!!")

	// 客户端配置
	clientConfig := DefaultHandshakeConfig()
	clientConfig.AuthMethod = AuthMethodPSK
	clientConfig.AuthManager = DefaultAuthManager(
		psk,
		nil, // 无token
		nil, // 无TOTP
	)

	// 服务端配置
	serverConfig := DefaultHandshakeConfig()
	serverConfig.AuthManager = DefaultAuthManager(
		psk,
		nil, // 无token
		nil, // 无TOTP
	)

	// 上下文
	ctx := context.Background()

	// 并行执行握手
	var clientSession, serverSession *Session
	var clientErr, serverErr error

	done := make(chan bool)

	// 客户端握手
	go func() {
		clientSession, clientErr = SimpleAuthenticate(ctx, clientConn, clientConfig)
		done <- true
	}()

	// 服务端握手
	go func() {
		serverSession, serverErr = SimpleAuthenticateServer(ctx, serverConn, serverConfig)
		done <- true
	}()

	// 等待两个握手完成
	<-done
	<-done

	// 检查错误
	if clientErr != nil {
		t.Errorf("客户端握手失败: %v", clientErr)
	}
	if serverErr != nil {
		t.Errorf("服务端握手失败: %v", serverErr)
	}

	// 检查会话
	if clientSession == nil {
		t.Error("客户端会话不应为nil")
	}
	if serverSession == nil {
		t.Error("服务端会话不应为nil")
	}

	// 验证会话ID匹配
	if !bytes.Equal(clientSession.ID, serverSession.ID) {
		t.Error("客户端和服务端会话ID应匹配")
	}

	// 验证会话密钥匹配（如果使用ECDH）
	if len(clientSession.Key) > 0 && len(serverSession.Key) > 0 {
		if !bytes.Equal(clientSession.Key, serverSession.Key) {
			t.Error("客户端和服务端会话密钥应匹配")
		}
	}
}

// newMemoryPipe 创建内存管道（模拟双向连接）
func newMemoryPipe() (io.ReadWriter, io.ReadWriter) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()

	// 客户端写入到w1，服务端从r1读取
	// 服务端写入到w2，客户端从r2读取
	return &pipeReadWriter{reader: r2, writer: w1},
		&pipeReadWriter{reader: r1, writer: w2}
}

// pipeReadWriter 实现 io.ReadWriter
type pipeReadWriter struct {
	reader *io.PipeReader
	writer *io.PipeWriter
}

func (p *pipeReadWriter) Read(buf []byte) (int, error) {
	return p.reader.Read(buf)
}

func (p *pipeReadWriter) Write(buf []byte) (int, error) {
	return p.writer.Write(buf)
}

func (p *pipeReadWriter) Close() error {
	p.reader.Close()
	p.writer.Close()
	return nil
}
