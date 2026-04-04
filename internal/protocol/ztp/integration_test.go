// Package ztp 实现 Ztp 隧道协议
package ztp

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/loopback"
)

// TestZtpOverLoopback 测试 Ztp 协议在环回传输上的端到端功能
func TestZtpOverLoopback(t *testing.T) {
	// 创建环回对
	pair := loopback.NewPair(10)
	defer pair.Close()

	// 启动客户端和服务器协程
	var wg sync.WaitGroup
	wg.Add(2)

	// 客户端协程
	go func() {
		defer wg.Done()

		// 获取客户端连接
		clientConn, err := pair.Dial()
		if err != nil {
			t.Errorf("客户端拨号失败: %v", err)
			return
		}
		defer clientConn.Close()

		// 创建客户端会话
		clientSession, err := NewSession(clientConn, DefaultSessionConfig())
		if err != nil {
			t.Errorf("创建客户端会话失败: %v", err)
			return
		}
		defer clientSession.Close()

		// 启动会话
		if err := clientSession.Start(); err != nil {
			t.Errorf("启动客户端会话失败: %v", err)
			return
		}

		// 打开流
		stream, err := clientSession.OpenStream()
		if err != nil {
			t.Errorf("客户端打开流失败: %v", err)
			return
		}
		defer stream.Close()

		// 写入测试数据
		testData := []byte("hello from client")
		n, err := stream.Write(testData)
		if err != nil {
			t.Errorf("客户端写入失败: %v", err)
			return
		}
		if n != len(testData) {
			t.Errorf("客户端写入字节数错误: 期望 %d, 得到 %d", len(testData), n)
		}

		// 关闭写入端
		stream.Close()

		t.Log("客户端完成")
	}()

	// 服务器协程
	go func() {
		defer wg.Done()

		// 获取服务器连接
		serverConn, err := pair.Accept()
		if err != nil {
			t.Errorf("服务器接受连接失败: %v", err)
			return
		}
		defer serverConn.Close()

		// 创建服务器会话
		serverSession, err := NewSession(serverConn, DefaultSessionConfig())
		if err != nil {
			t.Errorf("创建服务器会话失败: %v", err)
			return
		}
		defer serverSession.Close()

		// 启动会话
		if err := serverSession.Start(); err != nil {
			t.Errorf("启动服务器会话失败: %v", err)
			return
		}

		// 接受远程流
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		stream, err := serverSession.AcceptStream(ctx)
		if err != nil {
			t.Errorf("服务器接受流失败: %v", err)
			return
		}
		defer stream.Close()

		// 读取数据
		buf := make([]byte, 1024)
		n, err := stream.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("服务器读取失败: %v", err)
			return
		}

		expectedData := []byte("hello from client")
		if n != len(expectedData) {
			t.Errorf("服务器读取字节数错误: 期望 %d, 得到 %d", len(expectedData), n)
			return
		}

		if string(buf[:n]) != string(expectedData) {
			t.Errorf("服务器数据不匹配: 期望 %q, 得到 %q", string(expectedData), string(buf[:n]))
			return
		}

		// 尝试读取EOF
		_, err = stream.Read(buf)
		if err != io.EOF {
			t.Errorf("期望 EOF, 得到: %v", err)
		}

		t.Log("服务器完成")
	}()

	// 等待测试完成
	wg.Wait()
	t.Log("Ztp环回测试完成")
}

// TestZtpFlowControl 测试 Ztp 流控功能
func TestZtpFlowControl(t *testing.T) {
	// 创建环回对
	pair := loopback.NewPair(10)
	defer pair.Close()

	// 客户端连接
	clientConn, err := pair.Dial()
	if err != nil {
		t.Fatalf("客户端拨号失败: %v", err)
	}
	defer clientConn.Close()

	// 服务器连接
	serverConn, err := pair.Accept()
	if err != nil {
		t.Fatalf("服务器接受连接失败: %v", err)
	}
	defer serverConn.Close()

	// 创建会话
	clientSession, err := NewSession(clientConn, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建客户端会话失败: %v", err)
	}
	defer clientSession.Close()

	serverSession, err := NewSession(serverConn, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建服务器会话失败: %v", err)
	}
	defer serverSession.Close()

	// 启动会话
	if err := clientSession.Start(); err != nil {
		t.Fatalf("启动客户端会话失败: %v", err)
	}
	if err := serverSession.Start(); err != nil {
		t.Fatalf("启动服务器会话失败: %v", err)
	}

	// 客户端打开流
	clientStream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatalf("客户端打开流失败: %v", err)
	}
	defer clientStream.Close()

	// 服务器接受流
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	serverStream, err := serverSession.AcceptStream(ctx)
	if err != nil {
		t.Fatalf("服务器接受流失败: %v", err)
	}
	defer serverStream.Close()

	// 测试大数据传输（验证流控）
	largeData := make([]byte, 65536) // 64KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	// 客户端写入
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		n, err := clientStream.Write(largeData)
		if err != nil {
			t.Errorf("客户端写入大数据失败: %v", err)
			return
		}
		if n != len(largeData) {
			t.Errorf("客户端写入字节数错误: 期望 %d, 得到 %d", len(largeData), n)
		}
		clientStream.Close()
	}()

	go func() {
		defer wg.Done()
		buf := make([]byte, 65536)
		total := 0
		for total < len(largeData) {
			n, err := serverStream.Read(buf[total:])
			if err != nil && err != io.EOF {
				t.Errorf("服务器读取失败: %v", err)
				return
			}
			if n == 0 && err == io.EOF {
				break
			}
			total += n
		}

		if total != len(largeData) {
			t.Errorf("服务器读取总字节数错误: 期望 %d, 得到 %d", len(largeData), total)
			return
		}

		// 验证数据
		for i := 0; i < len(largeData); i++ {
			if buf[i] != largeData[i] {
				t.Errorf("数据不匹配 at index %d: 期望 %d, 得到 %d", i, largeData[i], buf[i])
				break
			}
		}
	}()

	wg.Wait()
	t.Log("流控测试完成")
}
