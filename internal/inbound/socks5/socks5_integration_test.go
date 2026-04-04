// 集成测试：SOCKS5服务器与自定义拨号器的协同工作
package socks5

import (
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/loopback"
	"github.com/SxAllyn/zpt/internal/outbound"
)

// TestSOCKS5WithCustomDialer 测试SOCKS5服务器使用自定义拨号器（环回出站）
func TestSOCKS5WithCustomDialer(t *testing.T) {
	// t.Skip("需要进一步调试环回出站集成") - 已修复，现在启用测试
	// 创建环回对
	pair := loopback.NewPair(1024)

	// 创建环回出站拨号器
	lo := outbound.NewLoopbackOutboundWithPair(pair)

	// 配置SOCKS5服务器使用自定义拨号器
	config := &Config{
		Addr:        "127.0.0.1:0", // 随机端口
		AuthMethods: []byte{0x00},  // 无认证
		RequireAuth: false,
		DialFunc: func(ctx context.Context, network, address string) (net.Conn, error) {
			t.Logf("自定义拨号器被调用: network=%s, address=%s", network, address)
			return lo.DialContext(ctx, network, address)
		},
	}

	server := NewServer(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 启动服务器
	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	// 获取实际监听地址
	serverAddr := server.Addr().String()

	// 启动目标服务器（模拟目标服务）
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		// 接受环回连接（模拟目标服务器）
		targetConn, err := pair.Accept()
		if err != nil {
			t.Errorf("目标服务器接受连接失败: %v", err)
			return
		}
		defer targetConn.Close()

		// 简单回显服务
		buf := make([]byte, 1024)
		n, err := targetConn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("目标服务器读取失败: %v", err)
			}
			return
		}
		// 回显数据
		_, err = targetConn.Write(buf[:n])
		if err != nil {
			t.Errorf("目标服务器写入失败: %v", err)
			return
		}
	}()

	// 等待目标服务器就绪
	time.Sleep(50 * time.Millisecond)

	// 客户端连接SOCKS5服务器
	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接SOCKS5服务器失败: %v", err)
	}
	defer clientConn.Close()

	// 发送SOCKS5握手（无认证）
	handshake := []byte{0x05, 0x01, 0x00}
	_, err = clientConn.Write(handshake)
	if err != nil {
		t.Fatalf("发送握手失败: %v", err)
	}

	// 读取握手回复
	reply := make([]byte, 2)
	_, err = io.ReadFull(clientConn, reply)
	if err != nil {
		t.Fatalf("读取握手回复失败: %v", err)
	}
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("握手回复异常: %v", reply)
	}

	// 发送CONNECT请求（域名类型，目标地址 test.local:8080）
	domain := "test.local"
	port := uint16(8080)
	connectReq := []byte{
		0x05,              // VER
		0x01,              // CMD CONNECT
		0x00,              // RSV
		0x03,              // ATYP 域名
		byte(len(domain)), // 域名长度
	}
	connectReq = append(connectReq, []byte(domain)...)
	portBytes := []byte{byte(port >> 8), byte(port & 0xFF)}
	connectReq = append(connectReq, portBytes...)

	_, err = clientConn.Write(connectReq)
	if err != nil {
		t.Fatalf("发送CONNECT请求失败: %v", err)
	}

	// 读取CONNECT回复
	connectReply := make([]byte, 10)
	_, err = io.ReadFull(clientConn, connectReply)
	if err != nil {
		t.Fatalf("读取CONNECT回复失败: %v", err)
	}
	if connectReply[0] != 0x05 || connectReply[1] != 0x00 {
		t.Fatalf("CONNECT回复失败: %v", connectReply)
	}

	// 发送测试数据
	testData := []byte("Hello, SOCKS5!")
	_, err = clientConn.Write(testData)
	if err != nil {
		t.Fatalf("发送测试数据失败: %v", err)
	}

	// 读取回显数据
	echoData := make([]byte, len(testData))
	_, err = io.ReadFull(clientConn, echoData)
	if err != nil {
		t.Fatalf("读取回显数据失败: %v", err)
	}

	// 验证数据一致性
	if string(echoData) != string(testData) {
		t.Errorf("回显数据不一致: 期望 %q, 得到 %q", testData, echoData)
	}

	// 等待目标服务器完成
	select {
	case <-targetDone:
	case <-time.After(1 * time.Second):
		t.Error("目标服务器未在超时内完成")
	}
}

// TestSOCKS5WithDefaultDialer 测试默认拨号器（net.Dial）行为
func TestSOCKS5WithDefaultDialer(t *testing.T) {
	// 创建一个简单的TCP回显服务器作为目标
	targetListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("创建目标监听器失败: %v", err)
	}
	defer targetListener.Close()

	targetAddr := targetListener.Addr().String()
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		conn, err := targetListener.Accept()
		if err != nil {
			t.Errorf("目标服务器接受连接失败: %v", err)
			return
		}
		defer conn.Close()
		// 简单回显服务：读取一次，写入相同数据
		buf := make([]byte, 1024)
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("目标服务器读取失败: %v", err)
			}
			return
		}
		_, err = conn.Write(buf[:n])
		if err != nil {
			t.Errorf("目标服务器写入失败: %v", err)
			return
		}
	}()

	// 配置SOCKS5服务器使用默认拨号器（DialFunc为nil）
	config := &Config{
		Addr:        "127.0.0.1:0",
		AuthMethods: []byte{0x00},
		RequireAuth: false,
		DialFunc:    nil, // 默认使用net.Dial
	}

	server := NewServer(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	serverAddr := server.Addr().String()

	// 客户端连接
	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接SOCKS5服务器失败: %v", err)
	}
	defer clientConn.Close()

	// 握手
	handshake := []byte{0x05, 0x01, 0x00}
	clientConn.Write(handshake)
	reply := make([]byte, 2)
	io.ReadFull(clientConn, reply)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("握手回复异常: %v", reply)
	}

	// 解析目标地址
	host, portStr, _ := net.SplitHostPort(targetAddr)
	port, _ := strconv.Atoi(portStr)

	// 发送CONNECT请求（IPv4类型）
	connectReq := []byte{
		0x05, // VER
		0x01, // CMD CONNECT
		0x00, // RSV
		0x01, // ATYP IPv4
	}
	ip := net.ParseIP(host).To4()
	connectReq = append(connectReq, ip...)
	connectReq = append(connectReq, byte(port>>8), byte(port&0xFF))

	clientConn.Write(connectReq)
	connectReply := make([]byte, 10)
	io.ReadFull(clientConn, connectReply)
	if connectReply[0] != 0x05 || connectReply[1] != 0x00 {
		t.Fatalf("CONNECT回复失败: %v", connectReply)
	}

	// 发送测试数据
	testData := []byte("Hello, default dialer!")
	clientConn.Write(testData)
	echoData := make([]byte, len(testData))
	io.ReadFull(clientConn, echoData)
	if string(echoData) != string(testData) {
		t.Errorf("回显数据不一致: 期望 %q, 得到 %q", testData, echoData)
	}

	// 关闭目标监听器以结束goroutine
	targetListener.Close()
	<-targetDone
}

// TestSOCKS5WithMockDialer 测试SOCKS5服务器与mock拨号器的集成
func TestSOCKS5WithMockDialer(t *testing.T) {
	// 创建一个简单的内存管道作为目标连接
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	// mock拨号器，返回预创建的管道连接
	mockDialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		t.Logf("Mock拨号器被调用: network=%s, address=%s", network, address)
		// 返回客户端端管道，服务器端管道将由目标服务器使用
		return clientPipe, nil
	}

	// 配置SOCKS5服务器使用mock拨号器
	config := &Config{
		Addr:        "127.0.0.1:0",
		AuthMethods: []byte{0x00},
		RequireAuth: false,
		DialFunc:    mockDialer,
	}

	server := NewServer(config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	serverAddr := server.Addr().String()

	// 目标服务器（使用服务器端管道）
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		// 简单回显服务
		buf := make([]byte, 1024)
		n, err := serverPipe.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("目标服务器读取失败: %v", err)
			}
			return
		}
		// 回显数据
		_, err = serverPipe.Write(buf[:n])
		if err != nil {
			t.Errorf("目标服务器写入失败: %v", err)
			return
		}
	}()

	// 等待目标服务器就绪
	time.Sleep(50 * time.Millisecond)

	// 客户端连接SOCKS5服务器
	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接SOCKS5服务器失败: %v", err)
	}
	defer clientConn.Close()

	// 握手
	handshake := []byte{0x05, 0x01, 0x00}
	clientConn.Write(handshake)
	reply := make([]byte, 2)
	io.ReadFull(clientConn, reply)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("握手回复异常: %v", reply)
	}

	// 发送CONNECT请求（IPv4类型，目标地址 192.168.1.1:80）
	connectReq := []byte{
		0x05,           // VER
		0x01,           // CMD CONNECT
		0x00,           // RSV
		0x01,           // ATYP IPv4
		192, 168, 1, 1, // IP地址
		0x00, 0x50, // 端口 80
	}
	clientConn.Write(connectReq)
	connectReply := make([]byte, 10)
	io.ReadFull(clientConn, connectReply)
	if connectReply[0] != 0x05 || connectReply[1] != 0x00 {
		t.Fatalf("CONNECT回复失败: %v", connectReply)
	}

	// 发送测试数据
	testData := []byte("Hello, mock dialer!")
	clientConn.Write(testData)
	echoData := make([]byte, len(testData))
	io.ReadFull(clientConn, echoData)
	if string(echoData) != string(testData) {
		t.Errorf("回显数据不一致: 期望 %q, 得到 %q", testData, echoData)
	}

	// 等待目标服务器完成
	select {
	case <-targetDone:
	case <-time.After(1 * time.Second):
		t.Error("目标服务器未在超时内完成")
	}
}
