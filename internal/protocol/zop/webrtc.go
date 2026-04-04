// Package zop 实现 Zpt 混淆协议
package zop

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
)

// webrtcTransport WebRTC伪装传输实现
type webrtcTransport struct {
	config      *Config
	mode        Mode
	stats       TransportStats
	createdAt   time.Time
	modeStartAt time.Time
	closed      bool
	// 底层连接和混淆器
	conn       io.ReadWriteCloser // 底层网络连接
	obfuscator Obfuscator         // WebRTC混淆器
	// 缓冲区管理
	readBuffer  []byte // 读取缓冲区（存储解混淆后的数据）
	writeBuffer []byte // 写入缓冲区（临时存储）
	// 模拟WebRTC连接状态
	dataChannelID  uint16
	sequenceNumber uint32
	peerConnected  bool
}

// newWebRTCTransport 创建WebRTC伪装传输
func newWebRTCTransport(config *Config, conn io.ReadWriteCloser) (Transport, error) {
	now := time.Now()

	// 创建对应的混淆器
	obfuscator, err := newWebRTCObfuscator(config)
	if err != nil {
		return nil, fmt.Errorf("创建WebRTC混淆器失败: %w", err)
	}

	return &webrtcTransport{
		config:        config,
		mode:          ModeWebRTC,
		createdAt:     now,
		modeStartAt:   now,
		stats:         TransportStats{},
		conn:          conn,
		obfuscator:    obfuscator,
		readBuffer:    GetBuffer(4096)[:0], // 从池中获取4KB缓冲区，重置长度为0
		writeBuffer:   GetBuffer(4096)[:0], // 从池中获取4KB缓冲区，重置长度为0
		dataChannelID: 1,
		peerConnected: true, // Mock中假设对等方已连接
	}, nil
}

// Read 读取数据（解封装WebRTC数据通道消息）
func (w *webrtcTransport) Read(p []byte) (n int, err error) {
	if w.closed {
		return 0, io.EOF
	}

	// 如果缓冲区有足够的解混淆数据，直接返回
	if len(w.readBuffer) > 0 {
		n = copy(p, w.readBuffer)
		w.readBuffer = w.readBuffer[n:]
		w.stats.BytesReceived += uint64(n)
		return n, nil
	}

	// 从底层连接读取原始数据
	rawBuf := GetBuffer(4096)
	rawN, err := w.conn.Read(rawBuf)
	if err != nil {
		PutBuffer(rawBuf)
		return 0, err
	}

	// 解混淆数据
	ctx := context.Background()
	deobfuscated, err := w.obfuscator.Deobfuscate(ctx, rawBuf[:rawN])
	// 归还读取缓冲区
	PutBuffer(rawBuf)
	if err != nil {
		return 0, fmt.Errorf("解混淆失败: %w", err)
	}

	// 存储到缓冲区并返回
	w.readBuffer = append(w.readBuffer, deobfuscated...)
	n = copy(p, w.readBuffer)
	w.readBuffer = w.readBuffer[n:]
	w.stats.BytesReceived += uint64(n)

	return n, nil
}

// Write 写入数据（封装为WebRTC数据通道消息）
func (w *webrtcTransport) Write(p []byte) (n int, err error) {
	if w.closed {
		return 0, io.ErrClosedPipe
	}

	if !w.peerConnected {
		return 0, fmt.Errorf("WebRTC对等方未连接")
	}

	// 混淆数据
	ctx := context.Background()
	obfuscated, err := w.obfuscator.Obfuscate(ctx, p)
	if err != nil {
		return 0, fmt.Errorf("混淆失败: %w", err)
	}

	// 写入底层连接
	_, err = w.conn.Write(obfuscated)
	// 写入后立即归还混淆器分配的缓冲区
	PutBuffer(obfuscated)
	if err != nil {
		return 0, err
	}

	w.stats.BytesSent += uint64(len(p))
	w.sequenceNumber++
	return len(p), nil // 返回原始数据长度，不是混淆后长度
}

// Close 关闭传输
func (w *webrtcTransport) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	w.peerConnected = false

	// 归还缓冲区到池
	if w.readBuffer != nil {
		PutBuffer(w.readBuffer)
		w.readBuffer = nil
	}
	if w.writeBuffer != nil {
		PutBuffer(w.writeBuffer)
		w.writeBuffer = nil
	}

	// 关闭底层连接
	if w.conn != nil {
		return w.conn.Close()
	}
	return nil
}

// Mode 返回当前伪装形态
func (w *webrtcTransport) Mode() Mode {
	return w.mode
}

// Switch 切换到新形态
func (w *webrtcTransport) Switch(ctx context.Context, newMode Mode) error {
	if w.closed {
		return io.ErrClosedPipe
	}

	// 检查是否支持目标形态
	supported := false
	for _, m := range w.config.EnabledModes {
		if m == newMode {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("不支持的目标形态: %v", newMode)
	}

	// 记录切换
	w.stats.SwitchCount++
	w.mode = newMode
	w.modeStartAt = time.Now()

	return nil
}

// GetStats 获取统计信息
func (w *webrtcTransport) GetStats() TransportStats {
	stats := w.stats
	stats.CurrentModeDuration = time.Since(w.modeStartAt)
	return stats
}

// webrtcObfuscator WebRTC混淆器实现
type webrtcObfuscator struct {
	config       *Config
	webrtcConfig WebRTCConfig
}

// newWebRTCObfuscator 创建WebRTC混淆器
func newWebRTCObfuscator(config *Config) (Obfuscator, error) {
	webrtcConfig := config.ModeConfigs[ModeWebRTC].WebRTC
	return &webrtcObfuscator{
		config:       config,
		webrtcConfig: webrtcConfig,
	}, nil
}

// Obfuscate 混淆数据为WebRTC格式
func (w *webrtcObfuscator) Obfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 构造WebRTC数据通道消息
	message := w.createDataChannelMessage(data)

	// 可选：封装为SDP offer/answer格式
	if w.webrtcConfig.SDPTemplate != "" {
		sdpMessage := w.embedInSDP(message)
		return sdpMessage, nil
	}

	return message, nil
}

// Deobfuscate 从WebRTC格式解混淆数据
func (w *webrtcObfuscator) Deobfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 解析WebRTC消息
	// 检查是否是SDP格式
	if w.isSDPFormat(data) {
		// 从SDP中提取数据通道消息
		extracted, err := w.extractFromSDP(data)
		if err != nil {
			return nil, err
		}
		data = extracted
	}

	// 解析数据通道消息
	// 解析 createDataChannelMessage 创建的格式
	const messageHeaderSize = 12 // 与 createDataChannelMessage 一致
	if len(data) < messageHeaderSize {
		return nil, fmt.Errorf("数据太短，无法解析WebRTC消息头")
	}

	// 验证头部格式（可选）
	// 检查 PPID 是否为 0x33（二进制数据）
	if data[7] != 0x33 {
		return nil, fmt.Errorf("无效的WebRTC消息PPID: %02x", data[7])
	}

	// 提取消息长度
	length := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	if len(data) < messageHeaderSize+int(length) {
		return nil, fmt.Errorf("WebRTC消息长度不符: 头中长度=%d, 实际长度=%d", length, len(data)-messageHeaderSize)
	}

	// 提取消息载荷
	return data[messageHeaderSize : messageHeaderSize+int(length)], nil
}

// GetMode 获取当前伪装形态
func (w *webrtcObfuscator) GetMode() Mode {
	return ModeWebRTC
}

// SwitchMode 切换伪装形态
func (w *webrtcObfuscator) SwitchMode(newMode Mode) error {
	// WebRTC混淆器不支持切换形态
	return fmt.Errorf("WebRTC混淆器不支持切换形态")
}

// createDataChannelMessage 创建WebRTC数据通道消息
func (w *webrtcObfuscator) createDataChannelMessage(data []byte) []byte {
	// WebRTC数据通道消息格式（简化版）
	// 实际实现需要构造完整的消息格式

	label := w.webrtcConfig.DataChannelLabel
	if label == "" {
		label = "data"
	}

	// 构造消息头
	// PPID (Payload Protocol Identifier): 51 表示二进制数据
	// 顺序号、时间戳等
	header := make([]byte, 12)
	header[0] = 0x80 // 版本等标志位
	header[1] = 0x00 // 保留
	header[4] = 0x00 // PPID高位
	header[5] = 0x00
	header[6] = 0x00
	header[7] = 0x33 // PPID = 51 (二进制)

	// 消息长度
	length := uint32(len(data))
	header[8] = byte(length >> 24)
	header[9] = byte(length >> 16)
	header[10] = byte(length >> 8)
	header[11] = byte(length & 0xFF)

	// 合并头和载荷，使用缓冲区池
	messageSize := len(header) + len(data)
	message := GetBuffer(messageSize)
	copy(message, header)
	copy(message[len(header):], data)

	// 返回适当大小的切片（调用者负责归还）
	return message[:messageSize]
}

// embedInSDP 将数据嵌入SDP格式
func (w *webrtcObfuscator) embedInSDP(data []byte) []byte {
	template := w.webrtcConfig.SDPTemplate
	if template == "" {
		// 默认SDP模板
		template = `v=0
o=- %s 2 IN IP4 127.0.0.1
s=-
t=0 0
a=group:BUNDLE 0
a=msid-semantic: WMS
m=application 9 UDP/DTLS/SCTP webrtc-datachannel
c=IN IP4 0.0.0.0
a=ice-ufrag:%s
a=ice-pwd:%s
a=ice-options:trickle
a=fingerprint:sha-256 %s
a=setup:actpass
a=mid:0
a=sctp-port:5000
a=max-message-size:262144
a=sendrecv
a=sctpmap:5000 webrtc-datachannel 256
a=candidate:1 1 UDP 2130706431 127.0.0.1 9 typ host
`
	}

	// 生成随机值
	sessionID := generateRandomID(16)
	iceUfrag := generateRandomID(8)
	icePwd := generateRandomID(24)
	fingerprint := generateRandomID(64)

	// 替换占位符
	sdp := fmt.Sprintf(template, sessionID, iceUfrag, icePwd, fingerprint)

	// 添加自定义属性携带数据
	encodedData := encodeBase64(data)
	sdp += fmt.Sprintf("a=data:%s\r\n", encodedData)

	return []byte(sdp)
}

// isSDPFormat 检查是否为SDP格式
func (w *webrtcObfuscator) isSDPFormat(data []byte) bool {
	// 简单检查：是否以"v=0"开头
	if len(data) >= 3 && string(data[:3]) == "v=0" {
		return true
	}
	return false
}

// extractFromSDP 从SDP中提取数据
func (w *webrtcObfuscator) extractFromSDP(data []byte) ([]byte, error) {
	// 解析SDP，查找"a=data:"属性
	sdpStr := string(data)
	lines := strings.Split(sdpStr, "\r\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "a=data:") {
			encoded := strings.TrimPrefix(line, "a=data:")
			return decodeBase64(encoded)
		}
	}
	return nil, fmt.Errorf("未找到数据属性")
}

// encodeBase64 简单Base64编码（Mock实现）
func encodeBase64(data []byte) string {
	// 简化实现
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	result := make([]byte, 0, (len(data)*4+2)/3)

	for i := 0; i < len(data); i += 3 {
		chunk := data[i:]
		if len(chunk) > 3 {
			chunk = chunk[:3]
		}

		// 编码逻辑
		var b uint32
		for j, v := range chunk {
			b |= uint32(v) << uint(16-j*8)
		}

		for j := 0; j < 4; j++ {
			if i*4/3+j < len(result) {
				idx := (b >> uint(18-j*6)) & 0x3F
				result = append(result, charset[idx])
			} else {
				result = append(result, '=')
			}
		}
	}

	return string(result)
}

// decodeBase64 简单Base64解码（Mock实现）
func decodeBase64(encoded string) ([]byte, error) {
	// 简化实现
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	charsetMap := make(map[byte]byte)
	for i, c := range charset {
		charsetMap[byte(c)] = byte(i)
	}

	// 移除填充
	encoded = strings.TrimRight(encoded, "=")

	result := make([]byte, 0, len(encoded)*3/4)
	var buffer uint32
	var bits int

	for _, c := range encoded {
		val, ok := charsetMap[byte(c)]
		if !ok {
			return nil, fmt.Errorf("非法Base64字符: %c", c)
		}

		buffer = (buffer << 6) | uint32(val)
		bits += 6

		if bits >= 8 {
			bits -= 8
			result = append(result, byte(buffer>>uint(bits)&0xFF))
		}
	}

	return result, nil
}
