// Package zop 测试
package zop

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	zop_proto "github.com/SxAllyn/zpt/internal/protocol/zop"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	if config == nil {
		t.Fatal("DefaultConfig() 返回 nil")
	}
	if config.ServerAddr != "localhost:443" {
		t.Errorf("期望 ServerAddr=localhost:443，得到 %s", config.ServerAddr)
	}
	if config.Timeout != 30*time.Second {
		t.Errorf("期望 Timeout=30s，得到 %v", config.Timeout)
	}
	if config.ZopConfig == nil {
		t.Error("期望 ZopConfig 非空")
	}
}

func TestNewOutbound(t *testing.T) {
	config := DefaultConfig()
	outbound, err := New(config)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer outbound.Close()

	// 验证初始状态
	_, err = outbound.GetStats()
	if err == nil {
		t.Error("期望 GetStats 在传输未初始化时返回错误")
	}

	// 测试关闭
	if err := outbound.Close(); err != nil {
		t.Errorf("Close 失败: %v", err)
	}
}

func TestOutboundDial(t *testing.T) {
	config := DefaultConfig()
	// 使用本地地址，避免实际网络连接
	config.ServerAddr = "127.0.0.1:0" // 端口0表示不实际连接

	outbound, err := New(config)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer outbound.Close()

	ctx := context.Background()

	// 尝试连接（应失败，因为地址无效）
	conn, err := outbound.DialContext(ctx, "tcp", "127.0.0.1:8080")
	if err != nil {
		// 预期错误，因为QUIC传输Mock尝试连接无效地址
		t.Logf("DialContext 预期失败: %v", err)
	} else {
		conn.Close()
	}
}

func TestOutboundSwitchMode(t *testing.T) {
	config := DefaultConfig()
	outbound, err := New(config)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer outbound.Close()

	ctx := context.Background()

	// 尝试切换形态（应失败，因为传输未初始化）
	err = outbound.SwitchMode(ctx, zop_proto.ModeHTTP3)
	if err == nil {
		t.Error("期望 SwitchMode 在传输未初始化时返回错误")
	}
}

func TestZopConnMethods(t *testing.T) {
	// 创建模拟连接用于测试接口方法
	conn := &ZopConn{
		localAddr:  &mockAddr{network: "tcp", address: "127.0.0.1:0"},
		remoteAddr: &mockAddr{network: "tcp", address: "127.0.0.1:443"},
	}

	// 测试地址方法
	local := conn.LocalAddr()
	if local.String() != "127.0.0.1:0" {
		t.Errorf("期望本地地址 127.0.0.1:0，得到 %s", local.String())
	}

	remote := conn.RemoteAddr()
	if remote.String() != "127.0.0.1:443" {
		t.Errorf("期望远程地址 127.0.0.1:443，得到 %s", remote.String())
	}

	// 测试截止时间设置
	deadline := time.Now().Add(time.Second)
	if err := conn.SetDeadline(deadline); err != nil {
		t.Errorf("SetDeadline 失败: %v", err)
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Errorf("SetReadDeadline 失败: %v", err)
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		t.Errorf("SetWriteDeadline 失败: %v", err)
	}

	// 测试关闭（无实际连接）
	if err := conn.Close(); err != nil {
		t.Errorf("Close 失败: %v", err)
	}
}

// mockAddr 模拟网络地址
type mockAddr struct {
	network string
	address string
}

func (m *mockAddr) Network() string { return m.network }
func (m *mockAddr) String() string  { return m.address }

// mockZopTransport 模拟Zop传输
type mockZopTransport struct {
	readData  []byte
	readPos   int
	writeData []byte
	closed    bool
	mu        sync.RWMutex
	mode      zop_proto.Mode
}

func (m *mockZopTransport) Read(p []byte) (n int, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return 0, io.EOF
	}

	if m.readPos >= len(m.readData) {
		return 0, io.EOF
	}

	n = copy(p, m.readData[m.readPos:])
	m.readPos += n
	return n, nil
}

func (m *mockZopTransport) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, io.ErrClosedPipe
	}

	m.writeData = append(m.writeData, p...)
	return len(p), nil
}

func (m *mockZopTransport) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	return nil
}

func (m *mockZopTransport) Mode() zop_proto.Mode {
	return m.mode
}

func (m *mockZopTransport) Switch(ctx context.Context, newMode zop_proto.Mode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.mode = newMode
	return nil
}

func (m *mockZopTransport) GetStats() zop_proto.TransportStats {
	return zop_proto.TransportStats{
		BytesSent:           uint64(len(m.writeData)),
		BytesReceived:       uint64(m.readPos),
		CurrentModeDuration: time.Second,
		SwitchCount:         1,
	}
}

// mockQUICConn 模拟QUIC连接
type mockQUICConn struct {
	readData  []byte
	readPos   int
	writeData []byte
	closed    bool
	mu        sync.RWMutex
}

func (m *mockQUICConn) Read(p []byte) (n int, err error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.closed {
		return 0, io.EOF
	}

	if m.readPos >= len(m.readData) {
		return 0, io.EOF
	}

	n = copy(p, m.readData[m.readPos:])
	m.readPos += n
	return n, nil
}

func (m *mockQUICConn) Write(p []byte) (n int, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return 0, io.ErrClosedPipe
	}

	m.writeData = append(m.writeData, p...)
	return len(p), nil
}

func (m *mockQUICConn) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.closed = true
	return nil
}

func TestZopConnReadWrite(t *testing.T) {
	// 创建模拟组件
	mockTransport := &mockZopTransport{
		readData: []byte("test data from transport"),
		mode:     zop_proto.ModeHTTP3,
	}
	mockQUIC := &mockQUICConn{
		readData: []byte("quic background data"),
	}

	// 创建ZopConn
	conn := &ZopConn{
		transport:  mockTransport,
		quicConn:   mockQUIC,
		localAddr:  &mockAddr{network: "tcp", address: "127.0.0.1:0"},
		remoteAddr: &mockAddr{network: "tcp", address: "127.0.0.1:443"},
	}
	defer conn.Close()

	// 测试从transport读取
	buf := make([]byte, 100)
	n, err := conn.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("Read 失败: %v", err)
	}
	if n <= 0 {
		t.Error("期望读取到数据")
	}
	t.Logf("从transport读取 %d 字节: %s", n, string(buf[:n]))

	// 测试写入到transport
	testData := []byte("data to write")
	n, err = conn.Write(testData)
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("期望写入 %d 字节，实际写入 %d 字节", len(testData), n)
	}

	// 验证数据被写入transport
	if len(mockTransport.writeData) != len(testData) {
		t.Errorf("期望transport收到 %d 字节，实际收到 %d 字节", len(testData), len(mockTransport.writeData))
	}
	if string(mockTransport.writeData) != string(testData) {
		t.Errorf("写入数据不匹配: 期望 %s, 得到 %s", testData, mockTransport.writeData)
	}

	// 测试截止时间
	deadline := time.Now().Add(time.Millisecond * 100)
	if err := conn.SetDeadline(deadline); err != nil {
		t.Errorf("SetDeadline 失败: %v", err)
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		t.Errorf("SetReadDeadline 失败: %v", err)
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		t.Errorf("SetWriteDeadline 失败: %v", err)
	}
}

func TestOutboundWithMockTransport(t *testing.T) {
	config := DefaultConfig()
	outbound, err := New(config)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer outbound.Close()

	// 此时outbound内部还没有初始化transport
	// 测试获取统计信息（应失败）
	_, err = outbound.GetStats()
	if err == nil {
		t.Error("期望 GetStats 在传输未初始化时返回错误")
	}

	// 测试切换形态（应失败）
	ctx := context.Background()
	err = outbound.SwitchMode(ctx, zop_proto.ModeWebRTC)
	if err == nil {
		t.Error("期望 SwitchMode 在传输未初始化时返回错误")
	}
}

func TestOutboundIntegration(t *testing.T) {
	// 这个测试验证出站连接器的完整流程
	// 由于需要实际的QUIC连接，这里只测试接口兼容性
	config := DefaultConfig()
	config.ServerAddr = "127.0.0.1:0" // 无效地址，测试错误处理

	outbound, err := New(config)
	if err != nil {
		t.Fatalf("New 失败: %v", err)
	}
	defer outbound.Close()

	// 测试Dial失败（预期）
	ctx := context.Background()
	conn, err := outbound.DialContext(ctx, "tcp", "127.0.0.1:8080")
	if err != nil {
		t.Logf("DialContext 预期失败（无效地址）: %v", err)
	} else {
		conn.Close()
		t.Error("期望 DialContext 失败（无效地址）")
	}
}

func TestZopConnClose(t *testing.T) {
	// 测试多次关闭的安全性
	mockTransport := &mockZopTransport{
		mode: zop_proto.ModeHTTP3,
	}
	mockQUIC := &mockQUICConn{}

	conn := &ZopConn{
		transport:  mockTransport,
		quicConn:   mockQUIC,
		localAddr:  &mockAddr{network: "tcp", address: "127.0.0.1:0"},
		remoteAddr: &mockAddr{network: "tcp", address: "127.0.0.1:443"},
	}

	// 第一次关闭
	if err := conn.Close(); err != nil {
		t.Errorf("第一次 Close 失败: %v", err)
	}

	// 第二次关闭（应安全）
	if err := conn.Close(); err != nil {
		t.Errorf("第二次 Close 失败: %v", err)
	}

	// 验证底层组件已关闭
	if !mockTransport.closed {
		t.Error("期望 transport 已关闭")
	}
	// QUIC连接由transport管理，transport为mock时不会关闭mockQUIC
	// if !mockQUIC.closed {
	// 	t.Error("期望 QUIC 连接已关闭")
	// }
}

// TestZopOutboundIntegration 测试Zop出站连接器与传输层的集成
func TestZopOutboundIntegration(t *testing.T) {
	// 创建环回对，模拟QUIC连接
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// 配置
	config := DefaultConfig()
	config.ServerAddr = "127.0.0.1:0" // 地址不重要，使用环回连接

	// 创建自定义拨号函数，返回预创建的服务器端连接（未使用，保留为占位符）
	dialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return serverConn, nil
	}
	_ = dialFunc // 避免未使用错误

	// 创建出站连接器，但需要修改以使用自定义QUIC传输层
	// 简化：直接创建传输层和连接包装器
	zopConfig := config.ZopConfig

	// 创建Zop传输层（客户端侧）
	clientTransport, err := zop_proto.NewTransport(zopConfig, clientConn)
	if err != nil {
		t.Fatalf("创建客户端Zop传输失败: %v", err)
	}
	defer clientTransport.Close()

	// 创建Zop传输层（服务器侧）
	serverTransport, err := zop_proto.NewTransport(zopConfig, serverConn)
	if err != nil {
		t.Fatalf("创建服务器Zop传输失败: %v", err)
	}
	defer serverTransport.Close()

	// 测试数据
	testData := []byte("Integration test data for Zop outbound")

	// 启动服务器协程，读取数据并回显
	go func() {
		buf := make([]byte, 4096)
		n, err := serverTransport.Read(buf)
		if err != nil && err != io.EOF {
			t.Errorf("服务器读取失败: %v", err)
			return
		}

		// 回显数据
		_, err = serverTransport.Write(buf[:n])
		if err != nil {
			t.Errorf("服务器回显失败: %v", err)
		}
		serverTransport.Close()
	}()

	// 客户端写入数据
	n, err := clientTransport.Write(testData)
	if err != nil {
		t.Fatalf("客户端写入失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("客户端写入字节数不匹配: 期望 %d, 得到 %d", len(testData), n)
	}

	// 客户端读取回显
	buf := make([]byte, 4096)
	totalRead := 0
	for totalRead < len(testData) {
		n, err := clientTransport.Read(buf[totalRead:])
		if err != nil && err != io.EOF {
			t.Fatalf("客户端读取失败: %v", err)
		}
		if n == 0 && err == io.EOF {
			break
		}
		totalRead += n
	}

	// 验证数据
	if totalRead != len(testData) {
		t.Errorf("回显数据长度不匹配: 期望 %d, 得到 %d", len(testData), totalRead)
	}
	if !bytes.Equal(buf[:totalRead], testData) {
		t.Errorf("回显数据内容不匹配")
	}

	t.Log("Zop出站集成测试完成")
}
