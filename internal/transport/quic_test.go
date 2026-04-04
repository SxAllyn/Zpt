// Package transport 测试
package transport

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// mockDialer 模拟拨号器，用于测试
type mockDialer struct {
	dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)
}

func (m *mockDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if m.dialFunc != nil {
		return m.dialFunc(ctx, network, addr)
	}
	return nil, io.EOF
}

func TestDefaultQUICConfig(t *testing.T) {
	config := DefaultQUICConfig()
	if config.Address != "" {
		t.Errorf("期望 Address 为空，得到 %s", config.Address)
	}
	if config.ServerName != "" {
		t.Errorf("期望 ServerName 为空，得到 %s", config.ServerName)
	}
	if config.InsecureSkipVerify {
		t.Error("期望 InsecureSkipVerify 为 false")
	}
	if len(config.NextProtos) != 1 || config.NextProtos[0] != "zpt-quic" {
		t.Errorf("期望 NextProtos 包含 'zpt-quic'，得到 %v", config.NextProtos)
	}
	if config.KeepAlivePeriod != 30*time.Second {
		t.Errorf("期望 KeepAlivePeriod=30s，得到 %v", config.KeepAlivePeriod)
	}
	if config.MaxIdleTimeout != 60*time.Second {
		t.Errorf("期望 MaxIdleTimeout=60s，得到 %v", config.MaxIdleTimeout)
	}
	if config.MaxIncomingStreams != 1024 {
		t.Errorf("期望 MaxIncomingStreams=1024，得到 %v", config.MaxIncomingStreams)
	}
	if config.DisablePathMTUDiscovery {
		t.Error("期望 DisablePathMTUDiscovery 为 false")
	}
	if config.HandshakeTimeout != 10*time.Second {
		t.Errorf("期望 HandshakeTimeout=10s，得到 %v", config.HandshakeTimeout)
	}
	if config.Timeout != 30*time.Second {
		t.Errorf("期望 Timeout=30s，得到 %v", config.Timeout)
	}
}

func TestNewQUICTransport(t *testing.T) {
	config := DefaultQUICConfig()
	transport := NewQUICTransport(config)

	if transport == nil {
		t.Fatal("NewQUICTransport 返回 nil")
	}

	// 测试初始状态
	if transport.IsConnected() {
		t.Error("期望初始状态未连接")
	}
	if transport.LocalAddr() != nil {
		t.Error("期望 LocalAddr 在未连接时返回 nil")
	}
	if transport.RemoteAddr() != nil {
		t.Error("期望 RemoteAddr 在未连接时返回 nil")
	}
}

func TestQUICTransport_Dial(t *testing.T) {
	// 创建模拟连接
	testConn := &mockConn{
		readData:   []byte("test data"),
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}

	dialCalled := false
	mockDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCalled = true
		if network != "tcp" {
			t.Errorf("期望 network='tcp'，得到 %s", network)
		}
		if addr != "127.0.0.1:443" {
			t.Errorf("期望 addr='127.0.0.1:443'，得到 %s", addr)
		}
		return testConn, nil
	}

	config := DefaultQUICConfig()
	config.Address = "127.0.0.1:443"
	transport := NewQUICTransportWithDialer(config, mockDialFunc)

	ctx := context.Background()
	err := transport.Dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	if !dialCalled {
		t.Error("期望拨号函数被调用")
	}

	if !transport.IsConnected() {
		t.Error("期望连接已建立")
	}

	// 测试地址方法
	localAddr := transport.LocalAddr()
	if localAddr == nil {
		t.Error("期望 LocalAddr 非空")
	}

	remoteAddr := transport.RemoteAddr()
	if remoteAddr == nil {
		t.Error("期望 RemoteAddr 非空")
	}
}

func TestQUICTransport_ReadWrite(t *testing.T) {
	testData := []byte("test read write data")
	testConn := &mockConn{
		readData:   testData,
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}

	mockDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return testConn, nil
	}

	config := DefaultQUICConfig()
	transport := NewQUICTransportWithDialer(config, mockDialFunc)

	ctx := context.Background()
	err := transport.Dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	// 测试读取
	buf := make([]byte, len(testData))
	n, err := transport.Read(buf)
	if err != nil {
		t.Fatalf("Read 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("期望读取 %d 字节，实际读取 %d 字节", len(testData), n)
	}
	if string(buf[:n]) != string(testData) {
		t.Errorf("读取数据不匹配: 期望 %s, 得到 %s", testData, buf[:n])
	}

	// 测试写入
	writeData := []byte("data to write")
	n, err = transport.Write(writeData)
	if err != nil {
		t.Fatalf("Write 失败: %v", err)
	}
	if n != len(writeData) {
		t.Errorf("期望写入 %d 字节，实际写入 %d 字节", len(writeData), n)
	}

	// 验证数据写入到连接
	if string(testConn.writeData) != string(writeData) {
		t.Errorf("写入数据不匹配: 期望 %s, 得到 %s", writeData, testConn.writeData)
	}
}

func TestQUICTransport_Deadlines(t *testing.T) {
	testConn := &mockConn{
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}

	mockDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return testConn, nil
	}

	config := DefaultQUICConfig()
	transport := NewQUICTransportWithDialer(config, mockDialFunc)

	ctx := context.Background()
	err := transport.Dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	// 测试截止时间设置
	deadline := time.Now().Add(time.Second)
	if err := transport.SetDeadline(deadline); err != nil {
		t.Errorf("SetDeadline 失败: %v", err)
	}
	if err := transport.SetReadDeadline(deadline); err != nil {
		t.Errorf("SetReadDeadline 失败: %v", err)
	}
	if err := transport.SetWriteDeadline(deadline); err != nil {
		t.Errorf("SetWriteDeadline 失败: %v", err)
	}

	// 验证连接收到了截止时间
	if !testConn.deadline.Equal(deadline) {
		t.Errorf("期望截止时间 %v，得到 %v", deadline, testConn.deadline)
	}
	if !testConn.readDeadline.Equal(deadline) {
		t.Errorf("期望读截止时间 %v，得到 %v", deadline, testConn.readDeadline)
	}
	if !testConn.writeDeadline.Equal(deadline) {
		t.Errorf("期望写截止时间 %v，得到 %v", deadline, testConn.writeDeadline)
	}
}

func TestQUICTransport_Close(t *testing.T) {
	testConn := &mockConn{
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}

	mockDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return testConn, nil
	}

	config := DefaultQUICConfig()
	transport := NewQUICTransportWithDialer(config, mockDialFunc)

	ctx := context.Background()
	err := transport.Dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("Dial 失败: %v", err)
	}

	// 测试关闭
	if err := transport.Close(); err != nil {
		t.Errorf("Close 失败: %v", err)
	}

	// 验证连接已关闭
	if !testConn.closed {
		t.Error("期望底层连接已关闭")
	}

	// 测试再次关闭（应安全）
	if err := transport.Close(); err != nil {
		t.Errorf("第二次 Close 失败: %v", err)
	}
}

func TestQUICTransport_Reconnect(t *testing.T) {
	dialCount := 0
	testConn := &mockConn{
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 12345},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 443},
	}

	mockDialFunc := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCount++
		return testConn, nil
	}

	config := DefaultQUICConfig()
	transport := NewQUICTransportWithDialer(config, mockDialFunc)

	ctx := context.Background()

	// 第一次连接
	err := transport.Dial(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("第一次 Dial 失败: %v", err)
	}

	if dialCount != 1 {
		t.Errorf("期望拨号次数=1，得到 %d", dialCount)
	}

	// 重新连接
	err = transport.Reconnect(ctx, "tcp", "127.0.0.1:443")
	if err != nil {
		t.Fatalf("Reconnect 失败: %v", err)
	}

	if dialCount != 2 {
		t.Errorf("期望拨号次数=2（重新连接后），得到 %d", dialCount)
	}

	// 验证仍然连接
	if !transport.IsConnected() {
		t.Error("期望重新连接后仍然连接")
	}
}

func TestQUICTransport_ReadWriteWithoutConnection(t *testing.T) {
	config := DefaultQUICConfig()
	transport := NewQUICTransport(config)

	// 测试未连接时读取
	buf := make([]byte, 10)
	_, err := transport.Read(buf)
	if err == nil {
		t.Error("期望 Read 在未连接时返回错误")
	}

	// 测试未连接时写入
	_, err = transport.Write([]byte("test"))
	if err == nil {
		t.Error("期望 Write 在未连接时返回错误")
	}

	// 测试未连接时设置截止时间
	err = transport.SetDeadline(time.Now())
	if err == nil {
		t.Error("期望 SetDeadline 在未连接时返回错误")
	}

	err = transport.SetReadDeadline(time.Now())
	if err == nil {
		t.Error("期望 SetReadDeadline 在未连接时返回错误")
	}

	err = transport.SetWriteDeadline(time.Now())
	if err == nil {
		t.Error("期望 SetWriteDeadline 在未连接时返回错误")
	}
}

// mockConn 模拟网络连接
type mockConn struct {
	readData      []byte
	readPos       int
	writeData     []byte
	closed        bool
	localAddr     net.Addr
	remoteAddr    net.Addr
	deadline      time.Time
	readDeadline  time.Time
	writeDeadline time.Time
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.EOF
	}

	if m.readPos >= len(m.readData) {
		return 0, io.EOF
	}

	n = copy(b, m.readData[m.readPos:])
	m.readPos += n
	return n, nil
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.closed {
		return 0, io.ErrClosedPipe
	}

	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return m.localAddr
}

func (m *mockConn) RemoteAddr() net.Addr {
	return m.remoteAddr
}

func (m *mockConn) SetDeadline(t time.Time) error {
	m.deadline = t
	m.readDeadline = t
	m.writeDeadline = t
	return nil
}

func (m *mockConn) SetReadDeadline(t time.Time) error {
	m.readDeadline = t
	return nil
}

func (m *mockConn) SetWriteDeadline(t time.Time) error {
	m.writeDeadline = t
	return nil
}
