package outbound

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/loopback"
)

func TestLoopbackOutboundNew(t *testing.T) {
	config := DefaultLoopbackConfig()
	lb := NewLoopbackOutbound(config)
	if lb == nil {
		t.Fatal("NewLoopbackOutbound返回nil")
	}
}

func TestLoopbackOutboundWithPair(t *testing.T) {
	pair := loopback.NewPair(1024)
	defer pair.Close()

	lb := NewLoopbackOutboundWithPair(pair)
	if lb == nil {
		t.Fatal("NewLoopbackOutboundWithPair返回nil")
	}

	if lb.GetPair() != pair {
		t.Error("GetPair返回的pair不一致")
	}
}

func TestLoopbackOutboundDial(t *testing.T) {
	lb := NewLoopbackOutbound(DefaultLoopbackConfig())
	defer lb.Close()

	// Dial应成功
	conn, err := lb.Dial(context.Background(), "tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("Dial失败: %v", err)
	}
	defer conn.Close()

	// 测试连接是否可用
	testData := []byte("test")
	go func() {
		conn.Write(testData)
	}()

	// 从pair的另一端读取
	serverConn, err := lb.GetPair().Accept()
	if err != nil {
		t.Fatalf("Accept失败: %v", err)
	}
	defer serverConn.Close()

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
}

func TestLoopbackOutboundDialContext(t *testing.T) {
	lb := NewLoopbackOutbound(DefaultLoopbackConfig())
	defer lb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := lb.DialContext(ctx, "tcp", "127.0.0.1:8080")
	if err != nil {
		t.Fatalf("DialContext失败: %v", err)
	}
	defer conn.Close()

	// 验证连接
	if conn == nil {
		t.Fatal("返回的连接为nil")
	}
}

func TestLoopbackOutboundHandle(t *testing.T) {
	lb := NewLoopbackOutbound(DefaultLoopbackConfig())
	defer lb.Close()

	// Handle应始终成功（环回出站不需要处理）
	conn := &mockConn{}
	err := lb.Handle(context.Background(), conn, "127.0.0.1:8080")
	if err != nil {
		t.Errorf("Handle失败: %v", err)
	}
}

func TestLoopbackOutboundClose(t *testing.T) {
	lb := NewLoopbackOutbound(DefaultLoopbackConfig())

	// Close应安全
	err := lb.Close()
	if err != nil {
		t.Errorf("Close失败: %v", err)
	}

	// 重复关闭应安全
	err = lb.Close()
	if err != nil {
		t.Errorf("重复关闭失败: %v", err)
	}
}

func TestLoopbackOutboundConcurrent(t *testing.T) {
	lb := NewLoopbackOutbound(DefaultLoopbackConfig())
	defer lb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 并发Dial
	errCh := make(chan error, 10)
	for i := 0; i < 5; i++ {
		go func(id int) {
			conn, err := lb.Dial(context.Background(), "tcp", "127.0.0.1:8080")
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
		conn, err := lb.GetPair().Accept()
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

type mockAddr struct{}

func (m *mockAddr) Network() string { return "mock" }
func (m *mockAddr) String() string  { return "mock:0" }
