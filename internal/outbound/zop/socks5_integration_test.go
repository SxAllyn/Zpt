// 集成测试：Zop出站连接器与SOCKS5入站服务器的协同工作
package zop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/inbound/socks5"
	"github.com/SxAllyn/zpt/internal/protocol/zop"
	"github.com/SxAllyn/zpt/internal/transport"
)

// TestZopWithSOCKS5Integration 测试Zop出站连接器与SOCKS5入站服务器的集成
func TestZopWithSOCKS5Integration(t *testing.T) {
	// 设置全局测试超时
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 创建环回对，模拟完整的QUIC连接链
	// clientPipe -> SOCKS5服务器端的QUIC连接（由Zop出站使用）
	// serverPipe -> 目标服务器端的QUIC连接（由模拟目标服务器使用）
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	// 配置Zop出站连接器
	zopConfig := DefaultConfig()
	zopConfig.ServerAddr = "127.0.0.1:0" // 地址不重要，使用环回连接

	// 创建自定义QUIC传输层，返回预创建的管道连接
	// 我们需要重写QUICTransport的Dial方法，使其返回我们的管道连接
	// 为此，我们创建一个自定义的dial函数
	customDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Logf("自定义QUIC拨号器被调用: network=%s, addr=%s", network, addr)
		// 返回客户端端管道，服务器端管道将由目标服务器使用
		return clientPipe, nil
	}

	// 创建Zop出站连接器，但需要注入自定义的QUIC传输层
	// 由于QUICTransport的创建在Outbound内部，我们需要修改测试策略
	// 简化方法：直接创建Zop传输层，而不是完整的Outbound
	// 但为了测试完整链，我们仍需要SOCKS5服务器调用Outbound.DialContext

	// 方案：创建自定义拨号器，内部创建Zop传输层
	zopDialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		t.Logf("Zop拨号器被调用: network=%s, address=%s", network, address)

		// 创建QUIC传输层（使用自定义拨号函数）
		quicConfig := zopConfig.QUICConfig
		quicTransport := transport.NewQUICTransportWithDialer(quicConfig, customDialFunc)

		// 建立QUIC连接（Mock）
		if err := quicTransport.Dial(ctx, "tcp", zopConfig.ServerAddr); err != nil {
			return nil, fmt.Errorf("建立QUIC连接失败: %w", err)
		}

		// 创建Zop传输层
		zopTransport, err := zop.NewTransport(zopConfig.ZopConfig, quicTransport)
		if err != nil {
			quicTransport.Close()
			return nil, fmt.Errorf("创建Zop传输失败: %w", err)
		}

		// 创建连接包装器
		conn := &ZopConn{
			transport: zopTransport,
			quicConn:  quicTransport,
			localAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
			remoteAddr: &net.TCPAddr{
				IP:   net.ParseIP("127.0.0.1"),
				Port: 443,
			},
		}

		return conn, nil
	}

	// 配置SOCKS5服务器使用Zop拨号器
	socks5Config := &socks5.Config{
		Addr:        "127.0.0.1:0", // 随机端口
		AuthMethods: []byte{0x00},  // 无认证
		RequireAuth: false,
		DialFunc:    zopDialer,
	}

	server := socks5.NewServer(socks5Config)

	// 启动SOCKS5服务器
	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	// 获取实际监听地址
	serverAddr := server.Addr().String()

	// 启动目标服务器（模拟目标服务，使用服务器端管道）
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)
		// 服务器端管道代表QUIC连接的远端
		// 我们需要创建Zop传输层来解混淆数据
		zopConfig := zop.DefaultConfig()
		serverTransport, err := zop.NewTransport(zopConfig, serverPipe)
		if err != nil {
			t.Errorf("创建服务器Zop传输失败: %v", err)
			return
		}
		defer serverTransport.Close()

		// 简单回显服务
		buf := make([]byte, 1024)
		n, err := serverTransport.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("目标服务器读取失败: %v", err)
			}
			return
		}
		// 回显数据
		_, err = serverTransport.Write(buf[:n])
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
	connectReq := []byte{
		0x05, // VER
		0x01, // CMD CONNECT
		0x00, // RSV
		0x03, // ATYP 域名
		10,   // 域名长度
	}
	domain := "test.local"
	port := uint16(8080)
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
	testData := []byte("Hello, Zop with SOCKS5!")
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
		t.Log("集成测试完成：数据成功通过SOCKS5 → Zop → 目标服务器链")
	case <-time.After(2 * time.Second):
		t.Error("目标服务器未在超时内完成")
	}
}

// TestZopSOCKS5Handshake 测试SOCKS5服务器与Zop拨号器的基本集成（仅握手）
func TestZopSOCKS5Handshake(t *testing.T) {
	// 设置测试超时
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// 创建环回对
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	// 创建自定义QUIC拨号函数
	customDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		t.Logf("自定义QUIC拨号器被调用: network=%s, addr=%s", network, addr)
		return clientPipe, nil
	}

	// 创建Zop拨号器
	zopDialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		t.Logf("Zop拨号器被调用: network=%s, address=%s", network, address)

		// 使用默认配置
		config := DefaultConfig()

		// 创建QUIC传输层
		quicTransport := transport.NewQUICTransportWithDialer(config.QUICConfig, customDialFunc)

		// 建立QUIC连接（Mock）
		if err := quicTransport.Dial(ctx, "tcp", config.ServerAddr); err != nil {
			return nil, fmt.Errorf("建立QUIC连接失败: %w", err)
		}

		// 创建Zop传输层
		zopTransport, err := zop.NewTransport(config.ZopConfig, quicTransport)
		if err != nil {
			quicTransport.Close()
			return nil, fmt.Errorf("创建Zop传输失败: %w", err)
		}

		// 创建连接包装器
		conn := &ZopConn{
			transport: zopTransport,
			quicConn:  quicTransport,
			localAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
			remoteAddr: &net.TCPAddr{
				IP:   net.ParseIP("127.0.0.1"),
				Port: 443,
			},
		}

		return conn, nil
	}

	// 配置SOCKS5服务器
	socks5Config := &socks5.Config{
		Addr:        "127.0.0.1:0",
		AuthMethods: []byte{0x00},
		RequireAuth: false,
		DialFunc:    zopDialer,
	}

	server := socks5.NewServer(socks5Config)

	// 启动SOCKS5服务器
	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	serverAddr := server.Addr().String()

	// 客户端连接SOCKS5服务器
	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接SOCKS5服务器失败: %v", err)
	}
	defer clientConn.Close()

	// 发送SOCKS5握手
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

	// 验证握手成功
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("握手回复异常: %v", reply)
	}

	t.Log("SOCKS5握手成功完成，Zop拨号器集成验证通过")

	// 注意：我们不发送CONNECT请求，因为这会触发实际拨号
	// 而我们的mock目标服务器没有设置
}

// TestZopSOCKS5IntegrationSimplified 简化的SOCKS5与Zop集成测试，使用同步协调
func TestZopSOCKS5IntegrationSimplified(t *testing.T) {
	// 设置测试超时
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 创建管道对
	clientPipe, serverPipe := net.Pipe()
	defer clientPipe.Close()
	defer serverPipe.Close()

	// 同步通道：用于协调拨号器调用和目标服务器启动
	dialerCalled := make(chan struct{})
	serverReady := make(chan struct{})

	// 创建Zop拨号器（使用同步信号）
	zopDialer := func(ctx context.Context, network, address string) (net.Conn, error) {
		t.Logf("Zop拨号器被调用: network=%s, address=%s", network, address)

		// 通知拨号器已调用
		select {
		case dialerCalled <- struct{}{}:
		default:
		}

		// 等待目标服务器就绪信号（避免竞态条件）
		select {
		case <-serverReady:
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		// 使用默认配置
		config := DefaultConfig()

		// 创建自定义QUIC拨号函数，返回clientPipe
		customDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
			return clientPipe, nil
		}

		// 创建QUIC传输层
		quicTransport := transport.NewQUICTransportWithDialer(config.QUICConfig, customDialFunc)

		// 建立QUIC连接（Mock）
		if err := quicTransport.Dial(ctx, "tcp", config.ServerAddr); err != nil {
			return nil, fmt.Errorf("建立QUIC连接失败: %w", err)
		}

		// 创建Zop传输层
		zopTransport, err := zop.NewTransport(config.ZopConfig, quicTransport)
		if err != nil {
			quicTransport.Close()
			return nil, fmt.Errorf("创建Zop传输失败: %w", err)
		}

		// 创建连接包装器
		conn := &ZopConn{
			transport: zopTransport,
			quicConn:  quicTransport,
			localAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
			remoteAddr: &net.TCPAddr{
				IP:   net.ParseIP("127.0.0.1"),
				Port: 443,
			},
		}

		return conn, nil
	}

	// 配置SOCKS5服务器
	socks5Config := &socks5.Config{
		Addr:        "127.0.0.1:0",
		AuthMethods: []byte{0x00},
		RequireAuth: false,
		DialFunc:    zopDialer,
	}

	server := socks5.NewServer(socks5Config)

	// 启动SOCKS5服务器
	err := server.Listen(ctx)
	if err != nil {
		t.Fatalf("启动SOCKS5服务器失败: %v", err)
	}
	defer server.Close()

	serverAddr := server.Addr().String()

	// 目标服务器goroutine（等待拨号器调用信号）
	targetDone := make(chan struct{})
	go func() {
		defer close(targetDone)

		// 等待拨号器调用
		select {
		case <-dialerCalled:
			t.Log("目标服务器: 收到拨号器调用信号")
		case <-time.After(5 * time.Second):
			t.Errorf("目标服务器: 等待拨号器调用超时")
			return
		}

		// 通知拨号器可以继续
		close(serverReady)

		// 创建服务器端Zop传输层（使用相同的配置）
		config := DefaultConfig()
		serverTransport, err := zop.NewTransport(config.ZopConfig, serverPipe)
		if err != nil {
			t.Errorf("创建服务器Zop传输失败: %v", err)
			return
		}
		defer serverTransport.Close()

		// 简单回显服务
		buf := make([]byte, 1024)
		n, err := serverTransport.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("目标服务器读取失败: %v", err)
			}
			return
		}

		// 回显数据
		_, err = serverTransport.Write(buf[:n])
		if err != nil {
			t.Errorf("目标服务器写入失败: %v", err)
			return
		}

		t.Logf("目标服务器: 完成回显 %d 字节", n)
	}()

	// 客户端连接SOCKS5服务器
	clientConn, err := net.Dial("tcp", serverAddr)
	if err != nil {
		t.Fatalf("客户端连接SOCKS5服务器失败: %v", err)
	}
	defer clientConn.Close()

	// 发送SOCKS5握手
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
	connectReq := []byte{
		0x05, // VER
		0x01, // CMD CONNECT
		0x00, // RSV
		0x03, // ATYP 域名
		10,   // 域名长度
	}
	domain := "test.local"
	port := uint16(8080)
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
	testData := []byte("Hello, simplified Zop integration!")
	_, err = clientConn.Write(testData)
	if err != nil {
		t.Fatalf("发送测试数据失败: %v", err)
	}
	t.Logf("客户端: 发送 %d 字节测试数据", len(testData))

	// 读取回显数据
	echoData := make([]byte, len(testData))
	_, err = io.ReadFull(clientConn, echoData)
	if err != nil {
		t.Fatalf("读取回显数据失败: %v", err)
	}

	// 验证数据一致性
	if string(echoData) != string(testData) {
		t.Errorf("回显数据不一致: 期望 %q, 得到 %q", testData, echoData)
	} else {
		t.Logf("客户端: 收到回显 %d 字节，数据一致", len(echoData))
	}

	// 等待目标服务器完成
	select {
	case <-targetDone:
		t.Log("简化集成测试完成：数据成功通过SOCKS5 → Zop链")
	case <-time.After(3 * time.Second):
		t.Error("目标服务器未在超时内完成")
	}
}
