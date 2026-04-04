// Package zop 实现 Zpt 混淆协议
package zop

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// doqTransport DNS over QUIC伪装传输实现
type doqTransport struct {
	config      *Config
	mode        Mode
	stats       TransportStats
	createdAt   time.Time
	modeStartAt time.Time
	closed      bool
	// 底层连接和混淆器
	conn       io.ReadWriteCloser // 底层网络连接
	obfuscator Obfuscator         // DoQ混淆器
	// 缓冲区管理
	readBuffer  []byte // 读取缓冲区（存储解混淆后的数据）
	writeBuffer []byte // 写入缓冲区（临时存储）
	// 模拟DoQ连接状态
	queryID   uint16
	messageID uint32
	dnsServer string
}

// newDoQTransport 创建DoQ伪装传输
func newDoQTransport(config *Config, conn io.ReadWriteCloser) (Transport, error) {
	now := time.Now()

	// 创建对应的混淆器
	obfuscator, err := newDoQObfuscator(config)
	if err != nil {
		return nil, fmt.Errorf("创建DoQ混淆器失败: %w", err)
	}

	doqConfig := config.ModeConfigs[ModeDoQ].DoQ
	return &doqTransport{
		config:      config,
		mode:        ModeDoQ,
		createdAt:   now,
		modeStartAt: now,
		stats:       TransportStats{},
		conn:        conn,
		obfuscator:  obfuscator,
		readBuffer:  GetBuffer(4096)[:0], // 从池中获取4KB缓冲区，重置长度为0
		writeBuffer: GetBuffer(4096)[:0], // 从池中获取4KB缓冲区，重置长度为0
		queryID:     1,
		dnsServer:   doqConfig.DNSServer,
	}, nil
}

// Read 读取数据（解封装DoQ DNS消息）
func (d *doqTransport) Read(p []byte) (n int, err error) {
	if d.closed {
		return 0, io.EOF
	}

	// 如果缓冲区有足够的解混淆数据，直接返回
	if len(d.readBuffer) > 0 {
		n = copy(p, d.readBuffer)
		d.readBuffer = d.readBuffer[n:]
		d.stats.BytesReceived += uint64(n)
		return n, nil
	}

	// 从底层连接读取原始数据
	rawBuf := GetBuffer(4096)
	rawN, err := d.conn.Read(rawBuf)
	if err != nil {
		PutBuffer(rawBuf)
		return 0, err
	}

	// 解混淆数据
	ctx := context.Background()
	deobfuscated, err := d.obfuscator.Deobfuscate(ctx, rawBuf[:rawN])
	// 归还读取缓冲区
	PutBuffer(rawBuf)
	if err != nil {
		return 0, fmt.Errorf("解混淆失败: %w", err)
	}

	// 存储到缓冲区并返回
	d.readBuffer = append(d.readBuffer, deobfuscated...)
	n = copy(p, d.readBuffer)
	d.readBuffer = d.readBuffer[n:]
	d.stats.BytesReceived += uint64(n)

	return n, nil
}

// Write 写入数据（封装为DoQ DNS消息）
func (d *doqTransport) Write(p []byte) (n int, err error) {
	if d.closed {
		return 0, io.ErrClosedPipe
	}

	// 混淆数据
	ctx := context.Background()
	obfuscated, err := d.obfuscator.Obfuscate(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("混淆失败: %w", err)
	}

	// 写入底层连接
	_, err = d.conn.Write(obfuscated)
	// 写入后立即归还混淆器分配的缓冲区
	PutBuffer(obfuscated)
	if err != nil {
		return 0, err
	}

	d.stats.BytesSent += uint64(len(p))
	d.messageID++
	return len(p), nil // 返回原始数据长度，不是混淆后长度
}

// Close 关闭传输
func (d *doqTransport) Close() error {
	if d.closed {
		return nil
	}
	d.closed = true

	// 归还缓冲区到池
	if d.readBuffer != nil {
		PutBuffer(d.readBuffer)
		d.readBuffer = nil
	}
	if d.writeBuffer != nil {
		PutBuffer(d.writeBuffer)
		d.writeBuffer = nil
	}

	// 关闭底层连接
	if d.conn != nil {
		return d.conn.Close()
	}
	return nil
}

// Mode 返回当前伪装形态
func (d *doqTransport) Mode() Mode {
	return d.mode
}

// Switch 切换到新形态
func (d *doqTransport) Switch(ctx context.Context, newMode Mode) error {
	if d.closed {
		return io.ErrClosedPipe
	}

	// 检查是否支持目标形态
	supported := false
	for _, m := range d.config.EnabledModes {
		if m == newMode {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("不支持的目标形态: %v", newMode)
	}

	// 记录切换
	d.stats.SwitchCount++
	d.mode = newMode
	d.modeStartAt = time.Now()

	return nil
}

// GetStats 获取统计信息
func (d *doqTransport) GetStats() TransportStats {
	stats := d.stats
	stats.CurrentModeDuration = time.Since(d.modeStartAt)
	return stats
}

// doqObfuscator DoQ混淆器实现
type doqObfuscator struct {
	config    *Config
	doqConfig DoQConfig
}

// newDoQObfuscator 创建DoQ混淆器
func newDoQObfuscator(config *Config) (Obfuscator, error) {
	doqConfig := config.ModeConfigs[ModeDoQ].DoQ
	return &doqObfuscator{
		config:    config,
		doqConfig: doqConfig,
	}, nil
}

// Obfuscate 混淆数据为DoQ格式
func (d *doqObfuscator) Obfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 构造DNS查询
	dnsQuery := d.createDNSQuery(data)

	// 封装为DNS over QUIC消息
	doqMessage := d.createDoQMessage(dnsQuery)

	return doqMessage, nil
}

// Deobfuscate 从DoQ格式解混淆数据
func (d *doqObfuscator) Deobfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 解析DNS over QUIC消息
	// 提取DNS响应载荷

	// 简单实现：跳过DoQ消息头
	const doqHeaderSize = 12 // DoQ消息头大小
	if len(data) <= doqHeaderSize {
		return nil, fmt.Errorf("数据太短，无法解析DoQ消息")
	}

	// 解析DNS消息
	dnsData := data[doqHeaderSize:]
	// 从DNS响应中提取数据
	extracted, err := d.extractFromDNSResponse(dnsData)
	if err != nil {
		return nil, err
	}

	return extracted, nil
}

// GetMode 获取当前伪装形态
func (d *doqObfuscator) GetMode() Mode {
	return ModeDoQ
}

// SwitchMode 切换伪装形态
func (d *doqObfuscator) SwitchMode(newMode Mode) error {
	// DoQ混淆器不支持切换形态
	return fmt.Errorf("DoQ混淆器不支持切换形态")
}

// createDNSQuery 创建DNS查询
func (d *doqObfuscator) createDNSQuery(data []byte) []byte {
	// DNS消息格式（简化版）
	// 实际实现需要构造完整的DNS消息

	queryType := d.doqConfig.QueryType
	if queryType == "" {
		queryType = "TXT" // 使用TXT记录携带数据
	}

	// 生成随机子域名
	subdomain := d.generateRandomSubdomain()

	// DNS消息头
	header := make([]byte, 12)
	header[0] = 0x00 // ID高位
	header[1] = 0x01 // ID低位
	header[2] = 0x01 // 标志：递归查询
	header[3] = 0x00
	header[4] = 0x00 // QDCOUNT = 1
	header[5] = 0x01
	header[6] = 0x00 // ANCOUNT = 0
	header[7] = 0x00
	header[8] = 0x00 // NSCOUNT = 0
	header[9] = 0x00
	header[10] = 0x00 // ARCOUNT = 0
	header[11] = 0x00

	// 域名部分
	domain := subdomain + ".example.com"
	domainParts := strings.Split(domain, ".")
	domainBytes := make([]byte, 0)
	for _, part := range domainParts {
		domainBytes = append(domainBytes, byte(len(part)))
		domainBytes = append(domainBytes, part...)
	}
	domainBytes = append(domainBytes, 0x00) // 结束符

	// 查询类型和类
	var qtype uint16
	switch queryType {
	case "A":
		qtype = 1
	case "AAAA":
		qtype = 28
	case "TXT":
		qtype = 16
	default:
		qtype = 16 // 默认TXT
	}

	qclass := uint16(1) // IN (Internet)

	typeClass := make([]byte, 4)
	typeClass[0] = byte(qtype >> 8)
	typeClass[1] = byte(qtype & 0xFF)
	typeClass[2] = byte(qclass >> 8)
	typeClass[3] = byte(qclass & 0xFF)

	// 数据作为TXT记录的附加部分
	additional := make([]byte, 0)
	if queryType == "TXT" {
		// TXT记录格式
		txtRecord := d.createTXTRecord(data)
		additional = txtRecord
	}

	// 合并所有部分
	dnsMessage := make([]byte, 0, len(header)+len(domainBytes)+len(typeClass)+len(additional))
	dnsMessage = append(dnsMessage, header...)
	dnsMessage = append(dnsMessage, domainBytes...)
	dnsMessage = append(dnsMessage, typeClass...)
	dnsMessage = append(dnsMessage, additional...)

	return dnsMessage
}

// createDoQMessage 创建DNS over QUIC消息
func (d *doqObfuscator) createDoQMessage(dnsData []byte) []byte {
	// DoQ消息格式（简化版）
	// 实际实现需要遵循RFC9250

	// DoQ消息头
	header := make([]byte, 12)
	header[0] = 0x00 // 版本高位
	header[1] = 0x01 // 版本低位 = DoQ版本1
	header[2] = 0x00 // 类型：查询
	header[3] = 0x00

	// 消息ID
	msgID := uint32(time.Now().Unix() & 0xFFFF)
	header[4] = byte(msgID >> 24)
	header[5] = byte(msgID >> 16)
	header[6] = byte(msgID >> 8)
	header[7] = byte(msgID & 0xFF)

	// 消息长度
	length := uint32(len(dnsData))
	header[8] = byte(length >> 24)
	header[9] = byte(length >> 16)
	header[10] = byte(length >> 8)
	header[11] = byte(length & 0xFF)

	// 合并头和DNS数据，使用缓冲区池
	messageSize := len(header) + len(dnsData)
	doqMessage := GetBuffer(messageSize)
	copy(doqMessage, header)
	copy(doqMessage[len(header):], dnsData)

	// 返回适当大小的切片（调用者负责归还）
	return doqMessage[:messageSize]
}

// extractFromDNSResponse 从DNS响应中提取数据
func (d *doqObfuscator) extractFromDNSResponse(dnsData []byte) ([]byte, error) {
	// 解析DNS消息格式（由createDNSQuery创建）
	// 格式：DNS头部(12) + 域名 + 类型/类(4) + [可选的TXT记录数据]

	if len(dnsData) < 12 {
		return nil, fmt.Errorf("DNS数据太短")
	}

	// 跳过DNS头部 (12字节)
	pos := 12

	// 跳过域名（以0结尾）
	for pos < len(dnsData) && dnsData[pos] != 0 {
		length := int(dnsData[pos])
		if pos+length+1 > len(dnsData) {
			return nil, fmt.Errorf("域名长度超出数据范围")
		}
		pos += length + 1 // 长度字节 + 标签数据
	}

	if pos >= len(dnsData) {
		return nil, fmt.Errorf("DNS格式错误：未找到域名结束符")
	}
	pos++ // 跳过结束符0

	// 跳过类型和类 (4字节)
	if pos+4 > len(dnsData) {
		return nil, fmt.Errorf("DNS数据不足，无法读取类型和类")
	}
	pos += 4

	// 剩余部分应该是TXT记录数据（由createTXTRecord创建）
	// 解析TXT记录格式：[长度字节][数据]...
	result := make([]byte, 0)

	for pos < len(dnsData) {
		// 读取长度字节
		if pos >= len(dnsData) {
			break
		}
		chunkLen := int(dnsData[pos])
		pos++

		// 检查是否有足够的数据
		if pos+chunkLen > len(dnsData) {
			return nil, fmt.Errorf("TXT记录长度不符: 需要%d字节，剩余%d字节", chunkLen, len(dnsData)-pos)
		}

		// 提取chunk数据
		if chunkLen > 0 {
			chunk := dnsData[pos : pos+chunkLen]
			result = append(result, chunk...)
		}
		pos += chunkLen
	}

	return result, nil
}

// generateRandomSubdomain 生成随机子域名
func (d *doqObfuscator) generateRandomSubdomain() string {
	template := d.doqConfig.SubdomainTemplate
	if template == "" {
		template = "{random}.d.{domain}.com"
	}

	randomPart := generateRandomID(8)
	domain := "example"

	result := strings.ReplaceAll(template, "{random}", randomPart)
	result = strings.ReplaceAll(result, "{domain}", domain)

	// 提取子域名部分
	parts := strings.Split(result, ".")
	if len(parts) > 0 {
		return parts[0]
	}
	return randomPart
}

// createTXTRecord 创建TXT记录
func (d *doqObfuscator) createTXTRecord(data []byte) []byte {
	// TXT记录格式
	// 每个字符串前有长度字节

	// 将数据分块（每块最大255字节）
	chunks := make([][]byte, 0)
	for i := 0; i < len(data); i += 255 {
		end := i + 255
		if end > len(data) {
			end = len(data)
		}
		chunks = append(chunks, data[i:end])
	}

	// 构造TXT记录
	txtRecord := make([]byte, 0)
	for _, chunk := range chunks {
		txtRecord = append(txtRecord, byte(len(chunk)))
		txtRecord = append(txtRecord, chunk...)
	}

	return txtRecord
}
