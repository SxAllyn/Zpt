// 代理接口处理器，实现 router.InterfaceHandler 接口
package tunproxy

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/SxAllyn/zpt/internal/outbound"
	"github.com/SxAllyn/zpt/internal/router"
	"github.com/SxAllyn/zpt/internal/tun"
)

// PacketSender 数据包发送接口
type PacketSender interface {
	// SendPacketToTUN 发送数据包到TUN设备
	SendPacketToTUN(packet *tun.Packet) error
}

// ProxyInterface 代理接口处理器
type ProxyInterface struct {
	dialer       outbound.Dialer
	timeout      time.Duration
	connTrack    *ConnectionTracker
	stats        *InterfaceStats
	packetSender PacketSender // 数据包发送器（用于发送回TUN设备）
	mu           sync.RWMutex
	connections  map[string]*ProxyConnection // 连接映射
}

// InterfaceStats 接口统计
type InterfaceStats struct {
	PacketsProcessed uint64
	PacketsForwarded uint64
	BytesForwarded   uint64
	Connections      uint64
	Errors           uint64
}

// TCPState TCP连接状态
type TCPState uint8

const (
	// TCP状态常量
	TCPStateClosed      TCPState = 0  // 连接已关闭
	TCPStateListen      TCPState = 1  // 监听状态（服务器）
	TCPStateSynSent     TCPState = 2  // SYN已发送（客户端）
	TCPStateSynReceived TCPState = 3  // SYN已接收（服务器）
	TCPStateEstablished TCPState = 4  // 连接已建立
	TCPStateFinWait1    TCPState = 5  // FIN等待1
	TCPStateFinWait2    TCPState = 6  // FIN等待2
	TCPStateCloseWait   TCPState = 7  // 关闭等待
	TCPStateClosing     TCPState = 8  // 正在关闭
	TCPStateLastAck     TCPState = 9  // 最后ACK
	TCPStateTimeWait    TCPState = 10 // 时间等待
)

// TCPFlags TCP标志位掩码
const (
	TCPFlagFIN uint8 = 0x01
	TCPFlagSYN uint8 = 0x02
	TCPFlagRST uint8 = 0x04
	TCPFlagPSH uint8 = 0x08
	TCPFlagACK uint8 = 0x10
	TCPFlagURG uint8 = 0x20
	TCPFlagECE uint8 = 0x40
	TCPFlagCWR uint8 = 0x80
)

// ProxyConnection 代理连接
type ProxyConnection struct {
	ID           string
	SrcIP        net.IP
	SrcPort      uint16
	DstIP        net.IP
	DstPort      uint16
	OutboundConn net.Conn
	CreatedAt    time.Time
	LastActive   time.Time
	Closed       bool

	// TCP状态机
	State TCPState // 当前TCP状态

	// 序列号跟踪
	ClientISN uint32 // 客户端初始序列号
	ServerISN uint32 // 服务器初始序列号
	ClientSeq uint32 // 客户端下一个发送序列号
	ClientAck uint32 // 客户端期望接收的序列号
	ServerSeq uint32 // 服务器下一个发送序列号
	ServerAck uint32 // 服务器期望接收的序列号

	// 窗口和选项
	ClientWindow uint16 // 客户端窗口大小
	ServerWindow uint16 // 服务器窗口大小

	// 统计数据
	BytesFromClient uint64 // 从客户端接收的字节数
	BytesToClient   uint64 // 发送给客户端的字节数
	BytesFromServer uint64 // 从服务器接收的字节数
	BytesToServer   uint64 // 发送给服务器的字节数

	// 超时和重传
	LastKeepAlive time.Time // 最后保活时间
}

// NewProxyInterface 创建代理接口处理器
func NewProxyInterface(dialer outbound.Dialer, timeout time.Duration, packetSender PacketSender) *ProxyInterface {
	return &ProxyInterface{
		dialer:       dialer,
		timeout:      timeout,
		connTrack:    NewConnectionTracker(1024),
		stats:        &InterfaceStats{},
		packetSender: packetSender,
		connections:  make(map[string]*ProxyConnection),
	}
}

// SendPacket 发送数据包（实现 router.InterfaceHandler）
func (pi *ProxyInterface) SendPacket(packet []byte) error {
	pi.mu.Lock()
	pi.stats.PacketsProcessed++
	pi.mu.Unlock()

	// 解析IP头部
	ipVersion := packet[0] >> 4
	var ipHeaderLen int
	var protocol byte
	var srcIP, dstIP net.IP

	if ipVersion == 4 {
		// IPv4
		ipHeaderLen = int((packet[0] & 0x0F) * 4)
		if len(packet) < ipHeaderLen {
			pi.recordError()
			return fmt.Errorf("IPv4数据包太短")
		}
		protocol = packet[9]
		srcIP = net.IP(packet[12:16])
		dstIP = net.IP(packet[16:20])
	} else if ipVersion == 6 {
		// IPv6（简化处理）
		ipHeaderLen = 40
		if len(packet) < ipHeaderLen {
			pi.recordError()
			return fmt.Errorf("IPv6数据包太短")
		}
		protocol = packet[6] // Next Header
		srcIP = net.IP(packet[8:24])
		dstIP = net.IP(packet[24:40])
	} else {
		pi.recordError()
		return fmt.Errorf("未知IP版本: %d", ipVersion)
	}

	// 只处理TCP协议
	if protocol != 6 { // TCP协议号
		// 忽略非TCP流量
		return nil
	}

	// 解析TCP头部
	if len(packet) < ipHeaderLen+20 {
		pi.recordError()
		return fmt.Errorf("TCP数据包太短")
	}

	tcpHeader := packet[ipHeaderLen:]
	srcPort := uint16(tcpHeader[0])<<8 | uint16(tcpHeader[1])
	dstPort := uint16(tcpHeader[2])<<8 | uint16(tcpHeader[3])
	seqNum := uint32(tcpHeader[4])<<24 | uint32(tcpHeader[5])<<16 | uint32(tcpHeader[6])<<8 | uint32(tcpHeader[7])
	ackNum := uint32(tcpHeader[8])<<24 | uint32(tcpHeader[9])<<16 | uint32(tcpHeader[10])<<8 | uint32(tcpHeader[11])
	dataOffset := (tcpHeader[12] >> 4) * 4
	tcpFlags := tcpHeader[13]

	// 计算TCP载荷
	var tcpPayload []byte
	tcpHeaderLen := int(dataOffset)
	if len(tcpHeader) >= tcpHeaderLen {
		tcpPayload = tcpHeader[tcpHeaderLen:]
	}

	// 生成连接ID
	connID := generateConnID(srcIP, srcPort, dstIP, dstPort)

	// 检查TCP标志
	isSYN := (tcpFlags & TCPFlagSYN) != 0
	isFIN := (tcpFlags & TCPFlagFIN) != 0
	isRST := (tcpFlags & TCPFlagRST) != 0
	isACK := (tcpFlags & TCPFlagACK) != 0
	isPSH := (tcpFlags & TCPFlagPSH) != 0

	pi.mu.Lock()
	defer pi.mu.Unlock()

	// 获取连接（如果存在）
	conn, exists := pi.connections[connID]

	if isSYN {
		// SYN包，建立新连接
		return pi.handleSYN(connID, srcIP, srcPort, dstIP, dstPort, seqNum)
	} else if isRST && exists {
		// RST包，立即关闭连接
		return pi.handleRST(conn)
	} else if isFIN && exists {
		// FIN包，开始关闭连接
		return pi.handleFIN(conn, ackNum, seqNum)
	} else if len(tcpPayload) > 0 {
		// 数据包，转发到现有连接
		return pi.handleData(connID, tcpPayload)
	} else if isACK && exists {
		// ACK包（可能带有PSH标志），更新序列号和状态
		return pi.handleACK(conn, ackNum, seqNum, isPSH)
	}

	// 其他情况，忽略
	return nil
}

// handleSYN 处理SYN包（建立新连接）
func (pi *ProxyInterface) handleSYN(connID string, srcIP net.IP, srcPort uint16, dstIP net.IP, dstPort uint16, clientSeq uint32) error {
	// 检查是否已存在连接
	if _, exists := pi.connections[connID]; exists {
		// 连接已存在，忽略重复SYN
		return nil
	}

	// 建立出站连接
	ctx, cancel := context.WithTimeout(context.Background(), pi.timeout)
	defer cancel()

	// 目标地址格式：ip:port
	targetAddr := net.JoinHostPort(dstIP.String(), fmt.Sprintf("%d", dstPort))

	conn, err := pi.dialer.DialContext(ctx, "tcp", targetAddr)
	if err != nil {
		pi.recordError()
		return fmt.Errorf("建立出站连接失败: %w", err)
	}

	// 创建连接记录
	proxyConn := &ProxyConnection{
		ID:           connID,
		SrcIP:        srcIP,
		SrcPort:      srcPort,
		DstIP:        dstIP,
		DstPort:      dstPort,
		OutboundConn: conn,
		CreatedAt:    time.Now(),
		LastActive:   time.Now(),
		Closed:       false,

		// TCP状态机初始化
		State: TCPStateSynReceived, // 已收到SYN，等待发送SYN-ACK

		// 序列号初始化
		ClientISN: clientSeq,     // 客户端初始序列号
		ClientSeq: clientSeq + 1, // SYN消耗一个序列号
		ClientAck: 0,             // 尚未收到服务器数据
		ServerISN: 0,             // 服务器初始序列号未知
		ServerSeq: 0,             // 服务器序列号未知
		ServerAck: clientSeq + 1, // 期望接收客户端的下一个序列号

		// 窗口大小（默认）
		ClientWindow: 65535,
		ServerWindow: 65535,

		// 统计
		BytesFromClient: 0,
		BytesToClient:   0,
		BytesFromServer: 0,
		BytesToServer:   0,

		// 超时
		LastKeepAlive: time.Now(),
	}

	pi.connections[connID] = proxyConn
	pi.stats.Connections++

	// 发送SYN-ACK响应给客户端
	if err := pi.sendSYNAck(proxyConn); err != nil {
		conn.Close()
		delete(pi.connections, connID)
		pi.recordError()
		return fmt.Errorf("发送SYN-ACK失败: %w", err)
	}

	// 更新状态为等待ACK
	proxyConn.State = TCPStateSynReceived

	// 启动数据转发goroutine
	go pi.forwardConnection(proxyConn)

	return nil
}

// handleClose 处理连接关闭
func (pi *ProxyInterface) handleClose(connID string) error {
	conn, exists := pi.connections[connID]
	if !exists {
		return nil // 连接不存在，忽略
	}

	if !conn.Closed {
		conn.Closed = true
		conn.OutboundConn.Close()
		delete(pi.connections, connID)
	}

	return nil
}

// handleACK 处理ACK包
func (pi *ProxyInterface) handleACK(conn *ProxyConnection, ackNum, seqNum uint32, isPSH bool) error {
	conn.LastActive = time.Now()

	// 根据当前状态处理ACK
	switch conn.State {
	case TCPStateSynReceived:
		// 在SYN_RECEIVED状态收到ACK，完成三次握手
		// 验证ACK号是否正确（应该确认我们的SYN）
		if ackNum == conn.ServerSeq {
			// ACK正确，连接建立
			conn.State = TCPStateEstablished
			// ServerAck已在handleSYN中设置，无需更新

			// 更新统计
			conn.BytesFromClient = 0 // 重置计数器
			conn.BytesToClient = 0   // 连接建立，尚无数据发送给客户端

			if pi.stats != nil {
				pi.stats.PacketsForwarded++
			}
		} else {
			// ACK不正确，可能是旧的重复包，忽略
			return nil
		}

	case TCPStateEstablished:
		// 在已建立连接状态收到ACK，更新确认号
		// 验证ACK号是否合理
		if ackNum > conn.ServerSeq {
			// ACK确认了新的数据，更新服务器序列号
			conn.ServerSeq = ackNum
		}

		// 更新客户端序列号
		if seqNum > 0 {
			conn.ClientAck = seqNum + 1
		}

	case TCPStateFinWait1:
		// 收到对FIN的ACK
		if ackNum == conn.ServerSeq {
			conn.State = TCPStateFinWait2
		}

	case TCPStateClosing:
		// 收到对FIN的ACK
		if ackNum == conn.ServerSeq {
			conn.State = TCPStateTimeWait
			// 启动定时器，稍后关闭连接
			go func() {
				time.Sleep(2 * time.Minute) // 2MSL
				pi.mu.Lock()
				delete(pi.connections, conn.ID)
				pi.mu.Unlock()
				conn.OutboundConn.Close()
			}()
		}

	case TCPStateLastAck:
		// 收到对FIN的ACK，连接可以关闭
		if ackNum == conn.ServerSeq {
			conn.State = TCPStateClosed
			conn.Closed = true
			conn.OutboundConn.Close()
			pi.mu.Lock()
			delete(pi.connections, conn.ID)
			pi.mu.Unlock()
		}

	default:
		// 其他状态，忽略ACK
		return nil
	}

	return nil
}

// handleRST 处理RST包
func (pi *ProxyInterface) handleRST(conn *ProxyConnection) error {
	// RST包立即关闭连接
	conn.State = TCPStateClosed
	conn.Closed = true
	conn.OutboundConn.Close()

	pi.mu.Lock()
	delete(pi.connections, conn.ID)
	pi.mu.Unlock()

	return nil
}

// handleFIN 处理FIN包
func (pi *ProxyInterface) handleFIN(conn *ProxyConnection, ackNum, seqNum uint32) error {
	conn.LastActive = time.Now()

	// 根据当前状态处理FIN
	switch conn.State {
	case TCPStateEstablished:
		// 客户端发送FIN，进入CLOSE_WAIT状态
		conn.State = TCPStateCloseWait

		// 更新序列号（FIN消耗一个序列号）
		conn.ClientSeq = seqNum + 1

		// 发送ACK响应
		if err := pi.sendACK(conn, conn.ServerSeq, conn.ClientSeq); err != nil {
			return err
		}

		// 然后代理应该发送自己的FIN（在适当的时候）
		// 这里简化处理：立即发送FIN
		go func() {
			time.Sleep(100 * time.Millisecond)
			pi.mu.Lock()
			if conn.State == TCPStateCloseWait {
				pi.sendFIN(conn)
			}
			pi.mu.Unlock()
		}()

	case TCPStateFinWait2:
		// 收到服务器的FIN（客户端已发送FIN，收到服务器的FIN）
		conn.State = TCPStateTimeWait

		// 发送ACK响应
		if err := pi.sendACK(conn, conn.ServerSeq, conn.ClientSeq); err != nil {
			return err
		}

		// 启动TIME_WAIT定时器
		go func() {
			time.Sleep(2 * time.Minute) // 2MSL
			pi.mu.Lock()
			delete(pi.connections, conn.ID)
			pi.mu.Unlock()
			conn.OutboundConn.Close()
		}()

	default:
		// 其他状态，发送ACK并关闭
		if err := pi.sendACK(conn, conn.ServerSeq, seqNum+1); err != nil {
			return err
		}
		conn.State = TCPStateClosed
		conn.Closed = true
		conn.OutboundConn.Close()
		pi.mu.Lock()
		delete(pi.connections, conn.ID)
		pi.mu.Unlock()
	}

	return nil
}

// handleData 处理数据包转发
func (pi *ProxyInterface) handleData(connID string, data []byte) error {
	conn, exists := pi.connections[connID]
	if !exists {
		return fmt.Errorf("连接不存在: %s", connID)
	}

	if conn.Closed {
		return fmt.Errorf("连接已关闭: %s", connID)
	}

	// 检查连接状态
	if conn.State != TCPStateEstablished {
		// 连接未建立，忽略数据
		return nil
	}

	// 转发数据到出站连接
	_, err := conn.OutboundConn.Write(data)
	if err != nil {
		pi.recordError()
		pi.handleClose(connID)
		return fmt.Errorf("转发数据失败: %w", err)
	}

	// 更新客户端序列号和统计
	conn.LastActive = time.Now()
	conn.ClientSeq += uint32(len(data))
	conn.BytesFromClient += uint64(len(data))
	conn.BytesToServer += uint64(len(data))

	pi.stats.PacketsForwarded++
	pi.stats.BytesForwarded += uint64(len(data))

	return nil
}

// forwardConnection 转发出站连接数据回TUN设备
func (pi *ProxyInterface) forwardConnection(conn *ProxyConnection) {
	defer func() {
		pi.mu.Lock()
		if !conn.Closed {
			conn.Closed = true
			conn.OutboundConn.Close()
			delete(pi.connections, conn.ID)
		}
		pi.mu.Unlock()
	}()

	buf := make([]byte, 65536)
	for {
		n, err := conn.OutboundConn.Read(buf)
		if err != nil {
			// 连接关闭或出错
			break
		}

		if n == 0 {
			continue
		}

		pi.mu.Lock()
		// 更新统计
		conn.LastActive = time.Now()
		pi.stats.PacketsForwarded++
		pi.stats.BytesForwarded += uint64(n)

		// 构造返回TUN设备的数据包
		if pi.packetSender != nil {
			// 使用当前序列号和确认号
			seqNum := conn.ServerSeq
			ackNum := conn.ClientAck
			flags := TCPFlagACK | TCPFlagPSH

			// 构造TCP数据包（从服务器到客户端）
			// 源IP: 服务器IP (conn.DstIP)
			// 目标IP: 客户端IP (conn.SrcIP)
			// 源端口: 服务器端口 (conn.DstPort)
			// 目标端口: 客户端端口 (conn.SrcPort)
			packet := constructTCPPacket(
				conn.DstIP,   // 源IP（服务器）
				conn.SrcIP,   // 目标IP（客户端）
				conn.DstPort, // 源端口（服务器端口）
				conn.SrcPort, // 目标端口（客户端端口）
				buf[:n],      // 载荷
				seqNum,       // 序列号
				ackNum,       // 确认号
				flags,        // TCP标志
			)

			// 发送数据包到TUN设备
			if err := pi.packetSender.SendPacketToTUN(packet); err != nil {
				// 记录错误但不中断连接
				pi.stats.Errors++
			}

			// 更新服务器序列号和统计
			conn.ServerSeq += uint32(n)
			conn.BytesToClient += uint64(n)
			conn.BytesFromServer += uint64(n)
		}
		pi.mu.Unlock()
	}
}

// sendSYNAck 发送SYN-ACK响应给客户端
func (pi *ProxyInterface) sendSYNAck(conn *ProxyConnection) error {
	if pi.packetSender == nil {
		return fmt.Errorf("数据包发送器未初始化")
	}

	// 生成服务器初始序列号（随机或递增）
	// 简化：使用时间戳作为基础
	serverISN := uint32(time.Now().UnixNano() & 0xFFFFFFFF)
	conn.ServerISN = serverISN
	conn.ServerSeq = serverISN + 1 // SYN消耗一个序列号

	// 构造SYN-ACK包
	// 源IP: 目标服务器IP（代理伪装成服务器）
	// 目标IP: 客户端IP
	// 源端口: 目标服务器端口
	// 目标端口: 客户端端口
	// 序列号: 服务器初始序列号
	// 确认号: 客户端下一个期望序列号（clientSeq + 1）
	// 标志位: SYN + ACK
	flags := TCPFlagSYN | TCPFlagACK

	packet := constructTCPPacket(
		conn.DstIP,     // 源IP（服务器）
		conn.SrcIP,     // 目标IP（客户端）
		conn.DstPort,   // 源端口（服务器端口）
		conn.SrcPort,   // 目标端口（客户端端口）
		nil,            // SYN-ACK没有载荷
		serverISN,      // 服务器初始序列号
		conn.ClientSeq, // 确认号：期望接收客户端的下一个序列号
		flags,          // SYN + ACK标志
	)

	// 发送数据包到TUN设备
	if err := pi.packetSender.SendPacketToTUN(packet); err != nil {
		return fmt.Errorf("发送SYN-ACK到TUN设备失败: %w", err)
	}

	// 更新统计
	conn.BytesToClient += uint64(len(packet.Data))

	return nil
}

// sendACK 发送ACK包
func (pi *ProxyInterface) sendACK(conn *ProxyConnection, ackNum, seqNum uint32) error {
	if pi.packetSender == nil {
		return fmt.Errorf("数据包发送器未初始化")
	}

	// 构造ACK包（无载荷）
	flags := TCPFlagACK
	packet := constructTCPPacket(
		conn.DstIP,   // 源IP（服务器）
		conn.SrcIP,   // 目标IP（客户端）
		conn.DstPort, // 源端口（服务器端口）
		conn.SrcPort, // 目标端口（客户端端口）
		nil,          // ACK没有载荷
		seqNum,       // 序列号
		ackNum,       // 确认号
		flags,        // ACK标志
	)

	// 发送数据包到TUN设备
	if err := pi.packetSender.SendPacketToTUN(packet); err != nil {
		return fmt.Errorf("发送ACK到TUN设备失败: %w", err)
	}

	// 更新统计
	conn.BytesToClient += uint64(len(packet.Data))

	return nil
}

// sendFIN 发送FIN包
func (pi *ProxyInterface) sendFIN(conn *ProxyConnection) error {
	if pi.packetSender == nil {
		return fmt.Errorf("数据包发送器未初始化")
	}

	// 构造FIN包（无载荷）
	flags := TCPFlagFIN | TCPFlagACK
	packet := constructTCPPacket(
		conn.DstIP,     // 源IP（服务器）
		conn.SrcIP,     // 目标IP（客户端）
		conn.DstPort,   // 源端口（服务器端口）
		conn.SrcPort,   // 目标端口（客户端端口）
		nil,            // FIN没有载荷
		conn.ServerSeq, // 序列号
		conn.ClientAck, // 确认号
		flags,          // FIN + ACK标志
	)

	// 发送数据包到TUN设备
	if err := pi.packetSender.SendPacketToTUN(packet); err != nil {
		return fmt.Errorf("发送FIN到TUN设备失败: %w", err)
	}

	// 更新统计
	conn.BytesToClient += uint64(len(packet.Data))

	// 更新服务器序列号（FIN消耗一个序列号）
	conn.ServerSeq++

	return nil
}

// GetInfo 获取接口信息（实现 router.InterfaceHandler）
func (pi *ProxyInterface) GetInfo() *router.InterfaceInfo {
	return &router.InterfaceInfo{
		Name:      "proxy",
		MTU:       1500,
		Addresses: []net.IPNet{},
		IsUp:      true,
	}
}

// recordError 记录错误
func (pi *ProxyInterface) recordError() {
	pi.mu.Lock()
	pi.stats.Errors++
	pi.mu.Unlock()
}

// constructTCPPacket 构造TCP/IP数据包
func constructTCPPacket(srcIP, dstIP net.IP, srcPort, dstPort uint16, payload []byte, seqNum, ackNum uint32, flags uint8) *tun.Packet {
	// 简化实现：构造IPv4 + TCP头部
	// IPv4头部：20字节
	// TCP头部：20字节（无选项）
	totalLen := 20 + 20 + len(payload)

	// 构造IPv4头部
	ipHeader := make([]byte, 20)
	ipHeader[0] = 0x45                // 版本4 + 头部长度5（5*4=20字节）
	ipHeader[1] = 0x00                // DSCP/ECN
	ipHeader[2] = byte(totalLen >> 8) // 总长度高8位
	ipHeader[3] = byte(totalLen)      // 总长度低8位
	ipHeader[4] = 0x00                // 标识高8位
	ipHeader[5] = 0x00                // 标识低8位
	ipHeader[6] = 0x40                // 标志（DF）+ 片偏移高8位
	ipHeader[7] = 0x00                // 片偏移低8位
	ipHeader[8] = 64                  // TTL（64）
	ipHeader[9] = 6                   // 协议（TCP）
	// 头部校验和先设为0
	ipHeader[10] = 0
	ipHeader[11] = 0
	// 源IP地址
	copy(ipHeader[12:16], srcIP.To4())
	// 目标IP地址
	copy(ipHeader[16:20], dstIP.To4())

	// 计算IPv4头部校验和
	ipChecksum := calculateChecksum(ipHeader)
	ipHeader[10] = byte(ipChecksum >> 8)
	ipHeader[11] = byte(ipChecksum)

	// 构造TCP头部
	tcpHeader := make([]byte, 20)
	tcpHeader[0] = byte(srcPort >> 8) // 源端口高8位
	tcpHeader[1] = byte(srcPort)      // 源端口低8位
	tcpHeader[2] = byte(dstPort >> 8) // 目标端口高8位
	tcpHeader[3] = byte(dstPort)      // 目标端口低8位
	tcpHeader[4] = byte(seqNum >> 24) // 序列号字节1
	tcpHeader[5] = byte(seqNum >> 16) // 序列号字节2
	tcpHeader[6] = byte(seqNum >> 8)  // 序列号字节3
	tcpHeader[7] = byte(seqNum)       // 序列号字节4
	tcpHeader[8] = byte(ackNum >> 24) // 确认号字节1
	tcpHeader[9] = byte(ackNum >> 16) // 确认号字节2
	tcpHeader[10] = byte(ackNum >> 8) // 确认号字节3
	tcpHeader[11] = byte(ackNum)      // 确认号字节4
	tcpHeader[12] = 0x50              // 数据偏移（5*4=20字节） + 保留位
	tcpHeader[13] = flags             // 标志位
	tcpHeader[14] = 0x00              // 窗口大小高8位
	tcpHeader[15] = 0x00              // 窗口大小低8位（简化）
	// TCP校验和先设为0
	tcpHeader[16] = 0
	tcpHeader[17] = 0
	tcpHeader[18] = 0x00 // 紧急指针高8位
	tcpHeader[19] = 0x00 // 紧急指针低8位

	// 计算TCP伪头部校验和
	pseudoHeader := make([]byte, 12)
	copy(pseudoHeader[0:4], srcIP.To4())              // 源IP
	copy(pseudoHeader[4:8], dstIP.To4())              // 目标IP
	pseudoHeader[8] = 0                               // 保留
	pseudoHeader[9] = 6                               // 协议（TCP）
	pseudoHeader[10] = byte((20 + len(payload)) >> 8) // TCP长度高8位
	pseudoHeader[11] = byte(20 + len(payload))        // TCP长度低8位

	// 组合数据用于校验和计算
	tcpForChecksum := make([]byte, 0, len(pseudoHeader)+len(tcpHeader)+len(payload))
	tcpForChecksum = append(tcpForChecksum, pseudoHeader...)
	tcpForChecksum = append(tcpForChecksum, tcpHeader...)
	tcpForChecksum = append(tcpForChecksum, payload...)

	tcpChecksum := calculateChecksum(tcpForChecksum)
	tcpHeader[16] = byte(tcpChecksum >> 8)
	tcpHeader[17] = byte(tcpChecksum)

	// 组合完整数据包
	packetData := make([]byte, 0, totalLen)
	packetData = append(packetData, ipHeader...)
	packetData = append(packetData, tcpHeader...)
	packetData = append(packetData, payload...)

	// 确定协议类型
	protocol := "IPv4"
	if len(srcIP) == 16 || len(dstIP) == 16 {
		protocol = "IPv6"
	}

	return &tun.Packet{
		Data:      packetData,
		Protocol:  protocol,
		Src:       srcIP,
		Dst:       dstIP,
		Timestamp: time.Now().UnixNano(),
	}
}

// calculateChecksum 计算IP/TCP校验和
func calculateChecksum(data []byte) uint16 {
	var sum uint32
	length := len(data)

	for i := 0; i < length-1; i += 2 {
		sum += uint32(data[i])<<8 | uint32(data[i+1])
	}

	// 如果长度为奇数，处理最后一个字节
	if length%2 == 1 {
		sum += uint32(data[length-1]) << 8
	}

	// 折叠进位
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}

	return ^uint16(sum)
}

// generateConnID 生成连接ID
func generateConnID(srcIP net.IP, srcPort uint16, dstIP net.IP, dstPort uint16) string {
	return fmt.Sprintf("%s:%d->%s:%d", srcIP, srcPort, dstIP, dstPort)
}

// GetStats 获取接口统计
func (pi *ProxyInterface) GetStats() *InterfaceStats {
	pi.mu.RLock()
	defer pi.mu.RUnlock()

	stats := *pi.stats
	return &stats
}

// Close 关闭所有连接
func (pi *ProxyInterface) Close() {
	pi.mu.Lock()
	defer pi.mu.Unlock()

	for id, conn := range pi.connections {
		if !conn.Closed {
			conn.Closed = true
			conn.OutboundConn.Close()
		}
		delete(pi.connections, id)
	}
}
