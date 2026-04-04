package inbound

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/loopback"
)

func TestLoopbackInboundNew(t *testing.T) {
	config := DefaultLoopbackConfig()
	lb := NewLoopbackInbound(config)
	if lb == nil {
		t.Fatal("NewLoopbackInbound返回nil")
	}
	defer lb.Close()

	addr := lb.Addr()
	if addr.Network() != "loopback" {
		t.Errorf("地址网络不符: 期望 loopback, 实际 %s", addr.Network())
	}
}

func TestLoopbackInboundWithPair(t *testing.T) {
	pair := loopback.NewPair(1024)
	defer pair.Close()

	lb := NewLoopbackInboundWithPair(pair)
	if lb == nil {
		t.Fatal("NewLoopbackInboundWithPair返回nil")
	}
	defer lb.Close()

	if lb.GetPair() != pair {
		t.Error("GetPair返回的pair不一致")
	}
}

func TestLoopbackInboundListen(t *testing.T) {
	lb := NewLoopbackInbound(DefaultLoopbackConfig())
	defer lb.Close()

	// Listen应始终成功（环回入站不需要实际监听）
	err := lb.Listen(context.Background())
	if err != nil {
		t.Errorf("Listen失败: %v", err)
	}
}

func TestLoopbackInboundAccept(t *testing.T) {
	lb := NewLoopbackInbound(DefaultLoopbackConfig())
	defer lb.Close()

	// 启动goroutine进行Dial
	connCh := make(chan net.Conn, 1)
	go func() {
		conn, err := lb.GetPair().Dial()
		if err != nil {
			// 错误情况，发送nil
			connCh <- nil
			return
		}
		connCh <- conn
	}()

	// Accept应能接收到连接
	serverConn, err := lb.Accept()
	if err != nil {
		t.Fatalf("Accept失败: %v", err)
	}
	defer serverConn.Close()

	// 检查goroutine结果
	select {
	case result := <-connCh:
		if result == nil {
			t.Fatal("Dial失败")
		}
		defer result.Close()
		// 测试数据传输
		testData := []byte("test")
		go func() {
			result.Write(testData)
		}()

		buf := make([]byte, len(testData))
		n, err := serverConn.Read(buf)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		if n != len(testData) {
			t.Fatalf("读取长度不符: 期望 %d, 实际 %d", len(testData), n)
		}
		if string(buf) != string(testData) {
			t.Fatalf("数据内容不符: 期望 %s, 实际 %s", testData, buf)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Accept超时")
	}
}

func TestLoopbackInboundClose(t *testing.T) {
	lb := NewLoopbackInbound(DefaultLoopbackConfig())

	// 关闭后Accept应失败
	err := lb.Close()
	if err != nil {
		t.Errorf("Close失败: %v", err)
	}

	_, err = lb.Accept()
	if err == nil {
		t.Error("关闭后Accept应返回错误")
	}

	// 重复关闭应安全
	err = lb.Close()
	if err != nil {
		t.Errorf("重复关闭应安全: %v", err)
	}
}

func TestLoopbackInboundHandle(t *testing.T) {
	lb := NewLoopbackInbound(DefaultLoopbackConfig())
	defer lb.Close()

	// 环回入站不支持外部连接处理
	// 创建模拟连接
	conn := &mockConn{}
	err := lb.Handle(context.Background(), conn)
	if err == nil {
		t.Error("Handle应返回错误")
	}
}

func TestLoopbackInboundConcurrent(t *testing.T) {
	lb := NewLoopbackInbound(DefaultLoopbackConfig())
	defer lb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 并发Accept和Dial
	errCh := make(chan error, 10)
	for i := 0; i < 5; i++ {
		go func(id int) {
			conn, err := lb.GetPair().Dial()
			if err != nil {
				errCh <- err
				return
			}
			defer conn.Close()
			conn.Write([]byte{byte(id)})
			errCh <- nil
		}(i)
	}

	// 接受连接并读取数据
	for i := 0; i < 5; i++ {
		conn, err := lb.Accept()
		if err != nil {
			t.Fatalf("Accept失败: %v", err)
		}
		defer conn.Close()

		buf := make([]byte, 1)
		_, err = conn.Read(buf)
		if err != nil {
			t.Fatalf("读取失败: %v", err)
		}
		t.Logf("接收到连接 %d", buf[0])
	}

	// 等待所有goroutine完成
	for i := 0; i < 5; i++ {
		select {
		case err := <-errCh:
			if err != nil {
				t.Errorf("goroutine失败: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("并发操作超时")
		}
	}
}

// mockConn 用于测试的模拟连接
type mockConn struct{}

func (m *mockConn) Read(b []byte) (n int, err error)   { return 0, nil }
func (m *mockConn) Write(b []byte) (n int, err error)  { return len(b), nil }
func (m *mockConn) Close() error                       { return nil }
func (m *mockConn) LocalAddr() net.Addr                { return &mockAddr{} }
func (m *mockConn) RemoteAddr() net.Addr               { return &mockAddr{} }
func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

type mockAddr struct{}

func (m *mockAddr) Network() string { return "mock" }
func (m *mockAddr) String() string  { return "mock:0" }
