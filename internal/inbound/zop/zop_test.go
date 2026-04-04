// Package zop 测试
package zop

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/protocol/zop"
	"github.com/SxAllyn/zpt/internal/transport"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config == nil {
		t.Fatal("DefaultConfig() 返回 nil")
	}
	if config.Addr != ":8443" {
		t.Errorf("期望 Addr=:8443，得到 %s", config.Addr)
	}
	if config.Timeout != 30*time.Second {
		t.Errorf("期望 Timeout=30s，得到 %v", config.Timeout)
	}
	if config.ZopConfig == nil {
		t.Error("期望 ZopConfig 非空")
	}
}

func TestNewServer(t *testing.T) {
	config := DefaultConfig()
	server := NewServer(config)
	if server == nil {
		t.Fatal("NewServer 返回 nil")
	}

	// 验证服务器可以关闭
	if err := server.Close(); err != nil {
		t.Errorf("Close 失败: %v", err)
	}
}

func TestServerListen(t *testing.T) {
	config := DefaultConfig()
	config.Addr = "127.0.0.1:0" // 随机端口
	server := NewServer(config)
	defer server.Close()

	ctx := context.Background()
	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}

	// 验证监听地址
	addr := server.Addr()
	if addr == nil {
		t.Error("期望 Addr 非空")
	}
}

func TestServerHandleConnection(t *testing.T) {
	// 创建服务器和客户端连接
	config := DefaultConfig()
	config.Addr = "127.0.0.1:0"
	config.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		// 模拟目标连接
		return &mockConn{}, nil
	}

	server := NewServer(config)
	defer server.Close()

	ctx := context.Background()
	if err := server.Listen(ctx); err != nil {
		t.Fatalf("Listen 失败: %v", err)
	}

	// 获取监听地址
	serverAddr := server.Addr().String()

	// 尝试建立客户端连接
	conn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接失败: %v", err)
	}
	defer conn.Close()

	// 发送一些数据
	testData := []byte("test")
	_, err = conn.Write(testData)
	if err != nil {
		t.Fatalf("发送数据失败: %v", err)
	}

	// 短暂等待处理
	time.Sleep(100 * time.Millisecond)
}

func TestConfigValidation(t *testing.T) {
	// 测试空配置
	server := NewServer(nil)
	if server == nil {
		t.Fatal("NewServer 返回 nil")
	}
	defer server.Close()

	// 测试无效地址
	config := &Config{
		Addr:       "invalid-address",
		QUICConfig: transport.DefaultQUICConfig(),
		ZopConfig:  zop.DefaultConfig(),
		Timeout:    30 * time.Second,
	}
	server2 := NewServer(config)
	defer server2.Close()

	ctx := context.Background()
	err := server2.Listen(ctx)
	if err == nil {
		t.Error("期望监听无效地址时失败")
	}
}

// mockConn 模拟连接
type mockConn struct {
	net.Conn
}

func (m *mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return &mockAddr{} }
func (m *mockConn) RemoteAddr() net.Addr               { return &mockAddr{} }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// mockAddr 模拟地址
type mockAddr struct{}

func (m *mockAddr) Network() string { return "tcp" }
func (m *mockAddr) String() string  { return "127.0.0.1:0" }
