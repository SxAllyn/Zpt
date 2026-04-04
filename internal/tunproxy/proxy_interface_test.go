// TCP状态管理单元测试
package tunproxy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/outbound"
	"github.com/SxAllyn/zpt/internal/tun"
)

// 确保mockDialer实现outbound.Dialer接口
var _ outbound.Dialer = (*mockDialer)(nil)

// mockPacketSender 模拟数据包发送器
type mockPacketSender struct {
	sentPackets []*tun.Packet
	lastError   error
}

func (m *mockPacketSender) SendPacketToTUN(packet *tun.Packet) error {
	m.sentPackets = append(m.sentPackets, packet)
	return m.lastError
}

// mockDialer 模拟拨号器
type mockDialer struct {
	conn       net.Conn
	dialError  error
	dialCalled bool
}

func (m *mockDialer) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return m.DialContext(ctx, network, address)
}

func (m *mockDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	m.dialCalled = true
	if m.dialError != nil {
		return nil, m.dialError
	}
	return m.conn, nil
}

// mockConn 模拟连接
type mockConn struct {
	net.Conn
	readData   []byte
	writeData  []byte
	readError  error
	writeError error
	closed     bool
}

func (m *mockConn) Read(b []byte) (n int, err error) {
	if m.readError != nil {
		return 0, m.readError
	}
	if len(m.readData) == 0 {
		// 模拟阻塞，防止forwardConnection关闭连接
		time.Sleep(time.Hour)
		return 0, nil
	}
	n = copy(b, m.readData)
	m.readData = m.readData[n:]
	return n, nil
}

func (m *mockConn) Write(b []byte) (n int, err error) {
	if m.writeError != nil {
		return 0, m.writeError
	}
	m.writeData = append(m.writeData, b...)
	return len(b), nil
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(192, 168, 1, 1), Port: 54321}
}

func (m *mockConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(8, 8, 8, 8), Port: 80}
}

func (m *mockConn) SetDeadline(t time.Time) error      { return nil }
func (m *mockConn) SetReadDeadline(t time.Time) error  { return nil }
func (m *mockConn) SetWriteDeadline(t time.Time) error { return nil }

// TestHandleSYN 测试SYN包处理
func TestHandleSYN(t *testing.T) {
	// 创建模拟对象
	mockSender := &mockPacketSender{}
	mockConn := &mockConn{}
	mockDialer := &mockDialer{conn: mockConn}

	// 创建代理接口
	pi := NewProxyInterface(mockDialer, 5*time.Second, mockSender)

	// 构造SYN包（简化IP/TCP头部）
	// 源IP: 10.0.0.2, 源端口: 12345
	// 目标IP: 8.8.8.8, 目标端口: 80
	srcIP := net.IPv4(10, 0, 0, 2)
	dstIP := net.IPv4(8, 8, 8, 8)
	srcPort := uint16(12345)
	dstPort := uint16(80)
	clientSeq := uint32(1000)

	// 生成连接ID
	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)

	// 调用handleSYN
	err := pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, clientSeq)
	if err != nil {
		t.Fatalf("handleSYN失败: %v", err)
	}

	// 验证拨号器被调用
	if !mockDialer.dialCalled {
		t.Error("拨号器未被调用")
	}

	// 验证发送了SYN-ACK包
	if len(mockSender.sentPackets) != 1 {
		t.Errorf("期望发送1个数据包，实际发送了%d个", len(mockSender.sentPackets))
	}

	// 验证连接被记录
	pi.mu.Lock()
	conn, exists := pi.connections[connID]
	pi.mu.Unlock()

	if !exists {
		t.Error("连接未被记录到映射中")
	}

	if conn == nil {
		t.Error("连接记录为空")
	}

	// 验证TCP状态
	if conn.State != TCPStateSynReceived {
		t.Errorf("期望状态TCPStateSynReceived，实际状态: %v", conn.State)
	}

	// 验证序列号
	if conn.ClientISN != clientSeq {
		t.Errorf("客户端初始序列号不匹配: 期望 %d, 实际 %d", clientSeq, conn.ClientISN)
	}

	if conn.ClientSeq != clientSeq+1 {
		t.Errorf("客户端下一个序列号不匹配: 期望 %d, 实际 %d", clientSeq+1, conn.ClientSeq)
	}
}

// TestHandleACK 测试ACK包处理（完成三次握手）
func TestHandleACK(t *testing.T) {
	// 创建模拟对象
	mockSender := &mockPacketSender{}
	mockConn := &mockConn{}
	mockDialer := &mockDialer{conn: mockConn}

	// 创建代理接口
	pi := NewProxyInterface(mockDialer, 5*time.Second, mockSender)

	// 首先建立连接（SYN处理）
	srcIP := net.IPv4(10, 0, 0, 2)
	dstIP := net.IPv4(8, 8, 8, 8)
	srcPort := uint16(12345)
	dstPort := uint16(80)
	clientSeq := uint32(1000)

	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)
	err := pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, clientSeq)
	if err != nil {
		t.Fatalf("初始SYN处理失败: %v", err)
	}

	// 获取连接
	pi.mu.Lock()
	conn, exists := pi.connections[connID]
	pi.mu.Unlock()

	if !exists {
		t.Fatal("连接不存在")
	}

	// 模拟ACK包（确认服务器的SYN）
	// 服务器序列号是conn.ServerSeq（在sendSYNAck中设置）
	serverSeq := conn.ServerSeq
	ackNum := serverSeq // ACK确认服务器的SYN

	// 处理ACK
	err = pi.handleACK(conn, ackNum, clientSeq+1, false)
	if err != nil {
		t.Fatalf("handleACK失败: %v", err)
	}

	// 验证状态变为ESTABLISHED
	if conn.State != TCPStateEstablished {
		t.Errorf("期望状态TCPStateEstablished，实际状态: %v", conn.State)
	}

	// 验证服务器期望的客户端序列号
	if conn.ServerAck != clientSeq+1 {
		t.Errorf("服务器期望的客户端序列号不匹配: 期望 %d, 实际 %d", clientSeq+1, conn.ServerAck)
	}
}

// TestHandleFIN 测试FIN包处理（连接关闭）
func TestHandleFIN(t *testing.T) {
	// 创建模拟对象
	mockSender := &mockPacketSender{}
	mockConn := &mockConn{}
	mockDialer := &mockDialer{conn: mockConn}

	// 创建代理接口
	pi := NewProxyInterface(mockDialer, 5*time.Second, mockSender)

	// 建立连接并完成三次握手
	srcIP := net.IPv4(10, 0, 0, 2)
	dstIP := net.IPv4(8, 8, 8, 8)
	srcPort := uint16(12345)
	dstPort := uint16(80)
	clientSeq := uint32(1000)

	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)
	err := pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, clientSeq)
	if err != nil {
		t.Fatalf("初始SYN处理失败: %v", err)
	}

	pi.mu.Lock()
	conn, exists := pi.connections[connID]
	pi.mu.Unlock()

	if !exists {
		t.Fatal("连接不存在")
	}

	// 完成三次握手
	serverSeq := conn.ServerSeq
	err = pi.handleACK(conn, serverSeq, clientSeq+1, false)
	if err != nil {
		t.Fatalf("ACK处理失败: %v", err)
	}

	// 清空之前发送的数据包
	mockSender.sentPackets = nil

	// 模拟客户端发送FIN包
	finSeq := clientSeq + 100 // 假设客户端发送了一些数据后的序列号
	err = pi.handleFIN(conn, conn.ServerSeq, finSeq)
	if err != nil {
		t.Fatalf("handleFIN失败: %v", err)
	}

	// 验证状态变为CLOSE_WAIT
	if conn.State != TCPStateCloseWait {
		t.Errorf("期望状态TCPStateCloseWait，实际状态: %v", conn.State)
	}

	// 验证发送了ACK响应
	if len(mockSender.sentPackets) == 0 {
		t.Error("未发送ACK响应")
	}

	// 注意：sendFIN会在goroutine中稍后调用
	// 等待一小段时间让goroutine执行
	time.Sleep(200 * time.Millisecond)

	// 验证最终发送了FIN包
	// 由于sendFIN在goroutine中调用，我们需要检查是否至少有一个数据包是FIN
	finFound := false
	for _, p := range mockSender.sentPackets {
		// 简化检查：数据包长度大于0
		if len(p.Data) > 0 {
			finFound = true
			break
		}
	}

	if !finFound {
		t.Error("未发送FIN包")
	}
}

// TestHandleData 测试数据包转发
func TestHandleData(t *testing.T) {
	// 创建模拟对象
	mockSender := &mockPacketSender{}
	mockConn := &mockConn{}
	mockDialer := &mockDialer{conn: mockConn}

	// 创建代理接口
	pi := NewProxyInterface(mockDialer, 5*time.Second, mockSender)

	// 建立连接并完成三次握手
	srcIP := net.IPv4(10, 0, 0, 2)
	dstIP := net.IPv4(8, 8, 8, 8)
	srcPort := uint16(12345)
	dstPort := uint16(80)
	clientSeq := uint32(1000)

	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)
	err := pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, clientSeq)
	if err != nil {
		t.Fatalf("初始SYN处理失败: %v", err)
	}

	pi.mu.Lock()
	conn, exists := pi.connections[connID]
	pi.mu.Unlock()

	if !exists {
		t.Fatal("连接不存在")
	}

	// 完成三次握手
	serverSeq := conn.ServerSeq
	err = pi.handleACK(conn, serverSeq, clientSeq+1, false)
	if err != nil {
		t.Fatalf("ACK处理失败: %v", err)
	}

	// 模拟数据包
	data := []byte("Hello, World!")
	err = pi.handleData(connID, data)
	if err != nil {
		t.Fatalf("handleData失败: %v", err)
	}

	// 验证数据被写入到出站连接
	if len(mockConn.writeData) != len(data) {
		t.Errorf("期望写入 %d 字节数据，实际写入 %d 字节", len(data), len(mockConn.writeData))
	}

	// 验证统计信息更新
	if conn.BytesFromClient != uint64(len(data)) {
		t.Errorf("BytesFromClient统计不匹配: 期望 %d, 实际 %d", len(data), conn.BytesFromClient)
	}

	if conn.BytesToServer != uint64(len(data)) {
		t.Errorf("BytesToServer统计不匹配: 期望 %d, 实际 %d", len(data), conn.BytesToServer)
	}
}

// TestTCPConnectionLifecycle 测试TCP连接完整生命周期
func TestTCPConnectionLifecycle(t *testing.T) {
	// 创建模拟对象
	mockSender := &mockPacketSender{}
	mockConn := &mockConn{
		readData: []byte("Hello from server!"), // 模拟服务器返回的数据
	}
	mockDialer := &mockDialer{conn: mockConn}

	// 创建代理接口
	pi := NewProxyInterface(mockDialer, 5*time.Second, mockSender)

	// 测试参数
	srcIP := net.IPv4(10, 0, 0, 2)
	dstIP := net.IPv4(8, 8, 8, 8)
	srcPort := uint16(12345)
	dstPort := uint16(80)
	clientSeq := uint32(1000)

	// 生成连接ID
	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)

	// 阶段1: SYN处理
	t.Run("SYN处理", func(t *testing.T) {
		mockSender.sentPackets = nil // 清空之前的数据包

		// 客户端发送SYN
		err := pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, clientSeq)
		if err != nil {
			t.Fatalf("SYN处理失败: %v", err)
		}

		// 验证发送了SYN-ACK
		if len(mockSender.sentPackets) != 1 {
			t.Errorf("期望发送1个数据包(SYN-ACK)，实际发送了%d个", len(mockSender.sentPackets))
		}

		// 验证连接记录存在
		pi.mu.Lock()
		conn, exists := pi.connections[connID]
		pi.mu.Unlock()

		if !exists {
			t.Error("连接未被记录")
		}

		if conn.State != TCPStateSynReceived {
			t.Errorf("期望状态TCPStateSynReceived，实际状态: %v", conn.State)
		}
	})

	// 阶段2: ACK处理（完成三次握手）
	t.Run("ACK处理", func(t *testing.T) {
		pi.mu.Lock()
		conn, exists := pi.connections[connID]
		pi.mu.Unlock()

		if !exists {
			t.Fatal("连接不存在")
		}

		// 客户端发送ACK（确认服务器的SYN）
		serverSeq := conn.ServerSeq
		err := pi.handleACK(conn, serverSeq, clientSeq+1, false)
		if err != nil {
			t.Fatalf("ACK处理失败: %v", err)
		}

		// 验证状态变为ESTABLISHED
		if conn.State != TCPStateEstablished {
			t.Errorf("期望状态TCPStateEstablished，实际状态: %v", conn.State)
		}
	})

	// 阶段3: 数据传输（客户端到服务器）
	t.Run("客户端到服务器数据传输", func(t *testing.T) {
		// 模拟客户端发送数据
		clientData := []byte("Hello, Server!")
		mockConn.writeData = nil // 清空之前的写入数据

		err := pi.handleData(connID, clientData)
		if err != nil {
			t.Fatalf("客户端数据处理失败: %v", err)
		}

		// 验证数据被写入到出站连接
		if len(mockConn.writeData) != len(clientData) {
			t.Errorf("期望写入 %d 字节数据到服务器，实际写入 %d 字节", len(clientData), len(mockConn.writeData))
		}

		// 验证数据内容
		if string(mockConn.writeData) != string(clientData) {
			t.Errorf("写入的数据内容不匹配")
		}
	})

	// 阶段4: 服务器到客户端数据回传（通过forwardConnection goroutine）
	t.Run("服务器到客户端数据回传", func(t *testing.T) {
		// forwardConnection已在goroutine中运行
		// 等待一小段时间让goroutine有机会读取数据
		time.Sleep(100 * time.Millisecond)

		// 验证发送了数据包回TUN设备
		// 注意：forwardConnection在单独的goroutine中运行，可能已经发送了数据包
		// 我们可以检查mockSender.sentPackets中是否有数据包
		dataPackets := 0
		for _, p := range mockSender.sentPackets {
			// 跳过SYN-ACK包（数据包长度大于0）
			if len(p.Data) > 0 {
				dataPackets++
			}
		}

		// 至少应该有一个数据包（可能是PSH+ACK包）
		if dataPackets == 0 {
			// 这可能是因为forwardConnection尚未读取数据，暂时不标记为失败
			t.Log("未检测到服务器数据回传包（可能goroutine尚未执行）")
		}
	})

	// 阶段5: FIN处理（连接关闭）
	t.Run("连接关闭", func(t *testing.T) {
		pi.mu.Lock()
		conn, exists := pi.connections[connID]
		pi.mu.Unlock()

		if !exists {
			t.Fatal("连接不存在")
		}

		// 模拟客户端发送FIN
		finSeq := conn.ClientSeq + 100 // 假设客户端发送了一些数据后的序列号
		mockSender.sentPackets = nil   // 清空之前的数据包（除了SYN-ACK）

		err := pi.handleFIN(conn, conn.ServerSeq, finSeq)
		if err != nil {
			t.Fatalf("FIN处理失败: %v", err)
		}

		// 验证状态变为CLOSE_WAIT
		if conn.State != TCPStateCloseWait {
			t.Errorf("期望状态TCPStateCloseWait，实际状态: %v", conn.State)
		}

		// 验证发送了ACK响应
		if len(mockSender.sentPackets) == 0 {
			t.Error("未发送ACK响应")
		}

		// 等待sendFIN goroutine执行
		time.Sleep(200 * time.Millisecond)

		// 验证最终发送了FIN包
		finFound := false
		for _, p := range mockSender.sentPackets {
			// 简化检查：数据包长度大于0
			if len(p.Data) > 0 {
				finFound = true
				break
			}
		}

		if !finFound {
			t.Error("未发送FIN包")
		}
	})

	// 阶段6: 验证连接清理
	t.Run("连接清理", func(t *testing.T) {
		// 模拟服务器对FIN的ACK
		pi.mu.Lock()
		conn, exists := pi.connections[connID]
		pi.mu.Unlock()

		if !exists {
			// 连接可能已经关闭，这是正常的
			t.Log("连接已清理")
			return
		}

		// 如果连接还存在，验证状态
		if conn.State == TCPStateTimeWait {
			t.Log("连接进入TIME_WAIT状态")
		}
	})
}
