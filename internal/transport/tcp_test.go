// Package transport 提供传输层实现
package transport

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// startTestServer 启动测试TCP服务器
func startTestServer(t *testing.T) (string, func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("启动测试服务器失败: %v", err)
	}

	// 简单的echo服务器
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(conn)
		}
	}()

	return listener.Addr().String(), func() {
		listener.Close()
	}
}

func TestTCPTransport_Dial(t *testing.T) {
	serverAddr, cleanup := startTestServer(t)
	defer cleanup()

	config := DefaultTCPConfig()
	config.Address = serverAddr
	transport := NewTCPTransport(config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := transport.Dial(ctx)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	if !transport.IsConnected() {
		t.Error("连接后 IsConnected 应返回 true")
	}

	// 测试读写
	testData := []byte("hello")
	n, err := transport.Write(testData)
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("写入字节数错误: 期望 %d, 得到 %d", len(testData), n)
	}

	// 读取回显
	buf := make([]byte, len(testData))
	n, err = transport.Read(buf)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("读取字节数错误: 期望 %d, 得到 %d", len(testData), n)
	}
	if string(buf) != string(testData) {
		t.Errorf("回显数据错误: 期望 %q, 得到 %q", string(testData), string(buf))
	}

	// 关闭连接
	err = transport.Close()
	if err != nil {
		t.Errorf("Close 失败: %v", err)
	}

	if transport.IsConnected() {
		t.Error("关闭后 IsConnected 应返回 false")
	}
}

func TestTCPTransport_Reconnect(t *testing.T) {
	serverAddr, cleanup := startTestServer(t)
	defer cleanup()

	config := DefaultTCPConfig()
	config.Address = serverAddr
	transport := NewTCPTransport(config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 第一次连接
	err := transport.Dial(ctx)
	if err != nil {
		t.Fatalf("第一次 Dial 失败: %v", err)
	}

	// 断开连接
	transport.Close()

	// 重新连接
	err = transport.Reconnect(ctx)
	if err != nil {
		t.Fatalf("Reconnect 失败: %v", err)
	}

	if !transport.IsConnected() {
		t.Error("重新连接后 IsConnected 应返回 true")
	}

	transport.Close()
}

func TestTCPTransport_SetDeadline(t *testing.T) {
	serverAddr, cleanup := startTestServer(t)
	defer cleanup()

	config := DefaultTCPConfig()
	config.Address = serverAddr
	transport := NewTCPTransport(config)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := transport.Dial(ctx)
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}
	defer transport.Close()

	// 设置截止时间
	err = transport.SetDeadline(time.Now().Add(100 * time.Millisecond))
	if err != nil {
		t.Errorf("SetDeadline 失败: %v", err)
	}

	// 设置读截止时间
	err = transport.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if err != nil {
		t.Errorf("SetReadDeadline 失败: %v", err)
	}

	// 设置写截止时间
	err = transport.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if err != nil {
		t.Errorf("SetWriteDeadline 失败: %v", err)
	}
}

func TestTCPTransport_NotConnected(t *testing.T) {
	transport := NewTCPTransport(DefaultTCPConfig())

	// 未连接时读取应返回错误
	buf := make([]byte, 10)
	_, err := transport.Read(buf)
	if err == nil {
		t.Error("未连接时 Read 应返回错误")
	}

	// 未连接时写入应返回错误
	_, err = transport.Write([]byte("test"))
	if err == nil {
		t.Error("未连接时 Write 应返回错误")
	}

	// 未连接时关闭应返回nil
	err = transport.Close()
	if err != nil {
		t.Errorf("未连接时 Close 应返回 nil, 得到: %v", err)
	}
}
