// Package zop 实现 Zpt 混淆协议
package zop

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/SxAllyn/zpt/internal/pool"
)

// HTTP/3 帧类型常量
const (
	http3FrameData         = 0x00 // DATA帧
	http3FrameHeaders      = 0x01 // HEADERS帧
	http3FrameWindowUpdate = 0x02 // WINDOW_UPDATE帧（流量控制扩展）
)

// 流量控制常量
const (
	defaultWindowSize     = 104857600 // 默认窗口大小（100MB）用于测试
	maxWindowSize         = 104857600 // 最大窗口大小（100MB）
	windowUpdateThreshold = 32768     // 窗口更新阈值（32KB）
	streamWriteThreshold  = 65536     // 流式写入阈值（64KB）
)

// hexDump 返回数据的十六进制表示（用于调试）
func hexDump(data []byte, maxBytes int) string {
	if len(data) == 0 {
		return "<空>"
	}

	var sb strings.Builder
	limit := len(data)
	if maxBytes > 0 && limit > maxBytes {
		limit = maxBytes
		sb.WriteString(fmt.Sprintf("(%d字节，显示前%d字节): ", len(data), maxBytes))
	}

	for i := 0; i < limit; i++ {
		sb.WriteString(fmt.Sprintf("%02x ", data[i]))
	}

	if limit < len(data) {
		sb.WriteString("...")
	}

	return sb.String()
}

// http3Transport HTTP/3伪装传输实现
type http3Transport struct {
	config      *Config
	mode        Mode
	stats       TransportStats
	createdAt   time.Time
	modeStartAt time.Time
	closed      bool
	// 底层连接和混淆器
	conn             io.ReadWriteCloser // 底层网络连接
	obfuscator       Obfuscator         // HTTP/3混淆器
	streamObfuscator StreamObfuscator   // 流式混淆器（可选）
	// 缓冲区管理
	readBuffer []byte // 读取缓冲区（存储解混淆后的数据）
	rawBuffer  []byte // 原始数据缓冲区（存储未解混淆的网络数据）
	// 模拟HTTP/3连接状态
	streamID       uint64
	nextStreamID   uint64
	hasSentHeaders bool   // 是否已发送HEADERS帧
	headersFrame   []byte // 预生成的HEADERS帧（零拷贝优化）
	// 流量控制
	maxRawBufferSize  int // 原始缓冲区最大大小（默认1MB）
	maxReadBufferSize int // 读取缓冲区最大大小（默认64KB）
	// 发送方流量控制
	sentBytes  atomic.Uint32 // 已发送字节数（已使用的窗口）
	windowSize atomic.Uint32 // 当前发送窗口大小（接收方授予的总信用）
	// 接收方流量控制
	receivedBytes atomic.Uint32 // 已接收字节数
	receiveWindow atomic.Uint32 // 当前接收窗口大小
	consumedBytes atomic.Uint32 // 自上次窗口更新以来消费的字节数
	// 零拷贝统计
	zeroCopyReadCount  atomic.Uint32 // 零拷贝读取次数
	zeroCopyWriteCount atomic.Uint32 // 零拷贝写入次数
	heapAllocCount     atomic.Uint32 // 堆分配次数（非池化）
}

// newHTTP3Transport 创建HTTP/3伪装传输
func newHTTP3Transport(config *Config, conn io.ReadWriteCloser) (Transport, error) {
	now := time.Now()

	// 创建对应的混淆器
	obfuscator, err := newHTTP3Obfuscator(config)
	if err != nil {
		return nil, fmt.Errorf("创建HTTP/3混淆器失败: %w", err)
	}

	transport := &http3Transport{
		config:            config,
		mode:              ModeHTTP3,
		createdAt:         now,
		modeStartAt:       now,
		stats:             TransportStats{},
		conn:              conn,
		obfuscator:        obfuscator,
		readBuffer:        GetBuffer(4096)[:0],  // 从池中获取4KB缓冲区，重置长度为0
		rawBuffer:         GetBuffer(65536)[:0], // 从池中获取64KB缓冲区，重置长度为0
		streamID:          1,
		nextStreamID:      3, // HTTP/3流ID：奇数客户端，偶数服务端
		hasSentHeaders:    false,
		maxRawBufferSize:  100 * 1024 * 1024, // 100MB
		maxReadBufferSize: 64 * 1024,         // 64KB
		// 流量控制字段使用零值初始化，下面显式设置初始值
	}

	// 检查混淆器是否支持流式接口
	if streamObf, ok := obfuscator.(StreamObfuscator); ok {
		transport.streamObfuscator = streamObf
		fmt.Printf("[HTTP3-INIT] 实例 %p 支持流式混淆器接口\n", transport)
	}

	// 初始化流量控制原子变量
	transport.windowSize.Store(defaultWindowSize)
	transport.receiveWindow.Store(uint32(transport.maxRawBufferSize))

	// 预生成HEADERS帧用于零拷贝优化
	if h3obf, ok := obfuscator.(*http3Obfuscator); ok {
		path := h3obf.generateRandomPath()
		transport.headersFrame = h3obf.createHeadersFrame(path)
		fmt.Printf("[HTTP3-INIT] 实例 %p 预生成HEADERS帧，长度=%d\n", transport, len(transport.headersFrame))
	} else {
		fmt.Printf("[HTTP3-INIT] 实例 %p 无法预生成HEADERS帧（混淆器类型不匹配）\n", transport)
	}

	return transport, nil
}

// Read 读取数据（解混淆HTTP/3格式数据）
func (h *http3Transport) Read(p []byte) (n int, err error) {
	if h.closed {
		fmt.Printf("[HTTP3-READ-CLOSED] 实例 %p 已关闭，返回EOF\n", h)
		return 0, io.EOF
	}
	fmt.Printf("[HTTP3-READ] 实例 %p 开始读取，用户缓冲区大小=%d，rawBuffer=%d，readBuffer=%d\n", h, len(p), len(h.rawBuffer), len(h.readBuffer))

	// 如果readBuffer有数据，直接返回
	if len(h.readBuffer) > 0 {
		n = copy(p, h.readBuffer)
		h.readBuffer = h.readBuffer[n:]
		h.stats.BytesReceived += uint64(n)
		return n, nil
	}

	// 循环直到有数据可返回或出错
	for len(h.readBuffer) == 0 {
		// 尝试从rawBuffer解析HTTP/3帧
		if len(h.rawBuffer) >= 3 {
			frameType := h.rawBuffer[0]
			length := uint16(h.rawBuffer[1])<<8 | uint16(h.rawBuffer[2])

			// 检查是否已有完整帧
			fmt.Printf("[HTTP3-READ-FRAME-CHECK] 实例 %p 帧类型=%02x, 长度=%d, rawBuffer=%d, 需要=%d\n", h, frameType, length, len(h.rawBuffer), 3+int(length))
			if len(h.rawBuffer) >= 3+int(length) {
				// 有完整帧，处理它
				fmt.Printf("[HTTP3-READ-FRAME-COMPLETE] 实例 %p 处理完整帧，类型=%02x\n", h, frameType)
				if frameType == http3FrameData { // DATA帧
					payloadStart := 3
					payloadEnd := 3 + int(length)
					payload := h.rawBuffer[payloadStart:payloadEnd]

					// 零拷贝优化：如果readBuffer为空且payload适合用户缓冲区，直接复制
					if len(h.readBuffer) == 0 && len(payload) <= len(p) {
						// 直接从rawBuffer复制到用户缓冲区
						n = copy(p, payload)
						h.stats.BytesReceived += uint64(n)

						// 从rawBuffer中移除已处理的帧
						remaining := len(h.rawBuffer) - payloadEnd
						if remaining > 0 {
							copy(h.rawBuffer, h.rawBuffer[payloadEnd:])
						}
						h.rawBuffer = h.rawBuffer[:remaining]

						// 流量控制：消费数据后立即发送WINDOW_UPDATE帧
						windowIncrement := uint32(length)
						frame := h.createWindowUpdateFrame(windowIncrement)
						_, err := h.conn.Write(frame)
						PutBuffer(frame)
						if err != nil {
							return 0, fmt.Errorf("发送WINDOW_UPDATE帧失败: %w", err)
						}

						// 直接返回，不通过readBuffer
						h.consumedBytes.Add(uint32(n))
						h.zeroCopyReadCount.Add(1)
						fmt.Printf("[HTTP3-READ-ZEROCOPY] 零拷贝读取 %d 字节，剩余 rawBuffer=%d\n", n, len(h.rawBuffer))
						return n, nil
					} else {
						// 传统方式：将数据添加到readBuffer
						h.readBuffer = append(h.readBuffer, payload...)
						fmt.Printf("[HTTP3-READ-BUFFERED] 缓冲读取 %d 字节，readBuffer=%d，剩余 rawBuffer=%d\n", len(payload), len(h.readBuffer), len(h.rawBuffer))

						// 从rawBuffer中移除已处理的帧
						remaining := len(h.rawBuffer) - payloadEnd
						if remaining > 0 {
							copy(h.rawBuffer, h.rawBuffer[payloadEnd:])
						}
						h.rawBuffer = h.rawBuffer[:remaining]

						// 流量控制：消费数据后立即发送WINDOW_UPDATE帧
						windowIncrement := uint32(length)
						frame := h.createWindowUpdateFrame(windowIncrement)
						_, err := h.conn.Write(frame)
						PutBuffer(frame)
						if err != nil {
							return 0, fmt.Errorf("发送WINDOW_UPDATE帧失败: %w", err)
						}

						break // 跳出循环，返回数据
					}
				} else if frameType == http3FrameHeaders { // HEADERS帧，忽略
					// 移除HEADERS帧
					frameEnd := 3 + int(length)
					remaining := len(h.rawBuffer) - frameEnd
					if remaining > 0 {
						copy(h.rawBuffer, h.rawBuffer[frameEnd:])
					}
					h.rawBuffer = h.rawBuffer[:remaining]

					// 继续处理下一个帧
					continue
				} else if frameType == http3FrameWindowUpdate { // WINDOW_UPDATE帧
					payloadStart := 3
					payloadEnd := 3 + int(length)
					payload := h.rawBuffer[payloadStart:payloadEnd]
					// 处理窗口更新帧
					if err := h.handleWindowUpdateFrame(payload); err != nil {
						return 0, fmt.Errorf("处理WINDOW_UPDATE帧失败: %w", err)
					}
					// 从rawBuffer中移除已处理的帧
					remaining := len(h.rawBuffer) - payloadEnd
					if remaining > 0 {
						copy(h.rawBuffer, h.rawBuffer[payloadEnd:])
					}
					h.rawBuffer = h.rawBuffer[:remaining]
					// 继续处理下一个帧（窗口更新不产生用户数据）
					continue
				} else {
					// 未知帧类型，错误
					return 0, fmt.Errorf("未知HTTP/3帧类型: %02x", frameType)
				}
			}
			// 帧不完整，继续读取更多数据
		}

		// 需要更多数据：从连接读取
		// 检查原始缓冲区是否超过限制
		if len(h.rawBuffer) >= h.maxRawBufferSize {
			// 缓冲区已满，跳过本次读取，等待消费者处理
			fmt.Printf("[BUFFER] rawBuffer 满: %d >= %d\n", len(h.rawBuffer), h.maxRawBufferSize)
			time.Sleep(time.Millisecond)
			continue
		}
		rawBuf := GetBuffer(65536)
		rawN, err := h.conn.Read(rawBuf)
		if err != nil {
			fmt.Printf("[READ-NET-ERROR] 连接读取错误: %v\n", err)
			PutBuffer(rawBuf)
			return 0, err
		}

		// 追加到rawBuffer，如果rawBuffer太大则扩展容量
		if cap(h.rawBuffer) < len(h.rawBuffer)+rawN {
			// 扩展缓冲区，避免频繁重新分配
			newSize := len(h.rawBuffer) + rawN
			if newSize < 65536 {
				newSize = 65536
			}
			newBuf := GetBuffer(newSize)
			copy(newBuf, h.rawBuffer)
			if len(h.rawBuffer) > 0 {
				PutBuffer(h.rawBuffer[:cap(h.rawBuffer)])
			}
			h.rawBuffer = newBuf[:len(h.rawBuffer)]
		}

		h.rawBuffer = append(h.rawBuffer, rawBuf[:rawN]...)
		PutBuffer(rawBuf)
	}

	// 返回readBuffer中的数据
	n = copy(p, h.readBuffer)
	h.readBuffer = h.readBuffer[n:]
	h.stats.BytesReceived += uint64(n)

	// 流量控制：更新消费字节数（窗口更新帧已在DATA帧处理时发送）
	// 保留消费字节数统计，但不再发送窗口更新帧
	if n > 0 {
		h.consumedBytes.Add(uint32(n))
		// 窗口更新帧已在每个DATA帧消费时发送，此处不再重复发送
	}

	return n, nil
}

// Write 写入数据（封装为HTTP/3帧）
func (h *http3Transport) Write(p []byte) (n int, err error) {
	if h.closed {
		fmt.Printf("[HTTP3-WRITE-CLOSED] 实例 %p 已关闭\n", h)
		return 0, io.ErrClosedPipe
	}
	sent := h.sentBytes.Load()
	window := h.windowSize.Load()
	available := window - sent
	fmt.Printf("[HTTP3-WRITE] 实例 %p 开始写入 %d 字节，hasSentHeaders=%v, sent=%d, window=%d, available=%d\n",
		h, len(p), h.hasSentHeaders, sent, window, available)

	// 处理可能的传入帧（如WINDOW_UPDATE帧）
	// 只在已发送HEADERS帧后处理，避免第一次写入时的超时问题
	if h.hasSentHeaders {
		if err := h.processIncomingFrames(); err != nil {
			fmt.Printf("[HTTP3-WRITE-PROCESS-FRAMES-ERROR] 实例 %p 处理传入帧失败: %v\n", h, err)
			// 不返回错误，继续写入
		}
	}

	// 分片大小：32KB，与测试中的读取缓冲区对齐
	const maxPayload = 32768

	if !h.hasSentHeaders {
		// 第一次写入：使用零拷贝技术发送HEADERS帧和DATA帧
		fmt.Printf("[HTTP3-WRITE-FIRST-ZEROCOPY] 实例 %p 第一次写入，使用零拷贝技术\n", h)

		// 检查是否应该使用流式处理
		if h.streamObfuscator != nil && len(p) > streamWriteThreshold {
			fmt.Printf("[HTTP3-WRITE-FIRST-STREAM] 实例 %p 数据量 %d > 阈值 %d，使用流式处理\n", h, len(p), streamWriteThreshold)
			return h.streamWrite(p, true)
		}

		// 如果有预生成的HEADERS帧，使用零拷贝优化
		if h.headersFrame != nil {
			// 计算HEADERS帧大小
			headersSize := len(h.headersFrame)
			// 计算总发送大小（HEADERS帧 + 原始数据）
			totalSendSize := headersSize + len(p)

			// 检查窗口是否足够
			if err := h.waitForWindow(uint32(totalSendSize)); err != nil {
				fmt.Printf("[HTTP3-WRITE-FIRST-WINDOW-ERROR] 实例 %p 窗口等待失败: %v\n", h, err)
				return 0, err
			}

			// 写入HEADERS帧（使用零拷贝）
			buf := GetBuffer(4096)
			defer PutBuffer(buf)
			fmt.Printf("[HTTP3-WRITE-FIRST-HEADERS] 实例 %p 写入HEADERS帧，长度=%d\n", h, headersSize)
			n, err := io.CopyBuffer(h.conn, bytes.NewReader(h.headersFrame), buf)
			if err != nil {
				fmt.Printf("[HTTP3-WRITE-FIRST-HEADERS-ERROR] 实例 %p HEADERS帧写入失败: %v\n", h, err)
				return 0, err
			}
			fmt.Printf("[HTTP3-WRITE-FIRST-HEADERS-SUCCESS] 实例 %p 写入 %d 字节\n", h, n)

			// 更新发送统计（HEADERS帧大小）
			h.sentBytes.Add(uint32(headersSize))

			// 标记已发送HEADERS帧
			h.hasSentHeaders = true

			// 使用零拷贝写入数据帧
			fmt.Printf("[HTTP3-WRITE-FIRST-DATA] 实例 %p 调用零拷贝写入数据 %d 字节\n", h, len(p))
			if err := h.writeDataFrameZeroCopy(p); err != nil {
				fmt.Printf("[HTTP3-WRITE-FIRST-DATA-ERROR] 实例 %p 数据写入失败: %v\n", h, err)
				return 0, err
			}

			// 更新统计
			h.stats.BytesSent += uint64(len(p))
			h.zeroCopyWriteCount.Add(1)
			fmt.Printf("[HTTP3-WRITE-FIRST-COMPLETE] 实例 %p 第一次写入完成\n", h)
			return len(p), nil
		} else {
			// 回退到传统方法
			fmt.Printf("[HTTP3-WRITE-FIRST-FALLBACK] 实例 %p 使用传统方法\n", h)
			ctx := context.Background()
			obfuscated, err := h.obfuscator.Obfuscate(ctx, p)
			if err != nil {
				return 0, fmt.Errorf("混淆失败: %w", err)
			}
			// 确保归还混淆器分配的缓冲区
			defer PutBuffer(obfuscated)
			h.hasSentHeaders = true

			// 直接写入混淆后的数据
			_, err = h.conn.Write(obfuscated)
			if err != nil {
				return 0, err
			}
			h.stats.BytesSent += uint64(len(p))
			h.sentBytes.Add(uint32(len(obfuscated)))
			return len(p), nil
		}
	}

	// 后续写入：使用零拷贝技术
	// 直接调用零拷贝写入函数，内部处理分片和流量控制

	// 检查是否应该使用流式处理
	if h.streamObfuscator != nil && len(p) > streamWriteThreshold {
		fmt.Printf("[HTTP3-WRITE-STREAM] 实例 %p 数据量 %d > 阈值 %d，使用流式处理\n", h, len(p), streamWriteThreshold)
		return h.streamWrite(p, false)
	}

	fmt.Printf("[HTTP3-WRITE-ZEROCOPY] 实例 %p 调用零拷贝写入 %d 字节\n", h, len(p))
	if err := h.writeDataFrameZeroCopy(p); err != nil {
		fmt.Printf("[HTTP3-WRITE-ZEROCOPY-ERROR] 实例 %p 零拷贝写入失败: %v\n", h, err)
		return 0, err
	}

	h.stats.BytesSent += uint64(len(p))
	h.sentBytes.Add(uint32(len(p)))
	h.zeroCopyWriteCount.Add(1)
	return len(p), nil // 返回原始数据长度，不是混淆后长度
}

// Close 关闭传输
func (h *http3Transport) Close() error {
	if h.closed {
		return nil
	}
	fmt.Printf("[HTTP3-CLOSE] 实例 %p 关闭传输\n", h)
	fmt.Printf("[HTTP3-STATS] 零拷贝读取次数: %d, 零拷贝写入次数: %d, 堆分配次数: %d\n",
		h.zeroCopyReadCount.Load(), h.zeroCopyWriteCount.Load(), h.heapAllocCount.Load())
	h.closed = true

	// 归还缓冲区到池
	if h.readBuffer != nil {
		PutBuffer(h.readBuffer)
		h.readBuffer = nil
	}
	if h.rawBuffer != nil {
		PutBuffer(h.rawBuffer)
		h.rawBuffer = nil
	}
	if h.headersFrame != nil {
		PutBuffer(h.headersFrame)
		h.headersFrame = nil
	}

	// 关闭底层连接
	if h.conn != nil {
		return h.conn.Close()
	}
	return nil
}

// Mode 返回当前伪装形态
func (h *http3Transport) Mode() Mode {
	return h.mode
}

// Switch 切换到新形态
func (h *http3Transport) Switch(ctx context.Context, newMode Mode) error {
	if h.closed {
		return io.ErrClosedPipe
	}

	// 检查是否支持目标形态
	supported := false
	for _, m := range h.config.EnabledModes {
		if m == newMode {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("不支持的目标形态: %v", newMode)
	}

	// 记录切换
	h.stats.SwitchCount++
	h.mode = newMode
	h.modeStartAt = time.Now()

	return nil
}

// GetStats 获取统计信息
func (h *http3Transport) GetStats() TransportStats {
	stats := h.stats
	stats.CurrentModeDuration = time.Since(h.modeStartAt)
	// 更新零拷贝统计
	stats.ZeroCopyReadCount = h.zeroCopyReadCount.Load()
	stats.ZeroCopyWriteCount = h.zeroCopyWriteCount.Load()
	stats.HeapAllocCount = h.heapAllocCount.Load()
	// 池分配计数从全局池获取
	stats.PoolAllocCount = pool.GetPoolAllocCount()
	return stats
}

// http3Obfuscator HTTP/3混淆器实现
type http3Obfuscator struct {
	config     *Config
	httpConfig HTTP3Config
}

// newHTTP3Obfuscator 创建HTTP/3混淆器
func newHTTP3Obfuscator(config *Config) (Obfuscator, error) {
	httpConfig := config.ModeConfigs[ModeHTTP3].HTTP3
	return &http3Obfuscator{
		config:     config,
		httpConfig: httpConfig,
	}, nil
}

// Obfuscate 混淆数据为HTTP/3格式
func (h *http3Obfuscator) Obfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 生成随机路径
	path := h.generateRandomPath()

	// 构造HTTP/3 HEADERS帧（只在第一次或需要时生成）
	headersFrame := h.createHeadersFrame(path)
	defer PutBuffer(headersFrame) // 确保归还HEADERS帧缓冲区

	// 分片数据，每片最大32KB，与测试中的读取缓冲区对齐
	const maxPayload = 32768

	// 使用bytes.Buffer逐步构建结果，避免一次性大内存预分配
	var buf bytes.Buffer
	// 预留合理容量以减少重新分配
	buf.Grow(len(headersFrame) + len(data) + (len(data)+maxPayload-1)/maxPayload*3)

	// 写入HEADERS帧
	buf.Write(headersFrame)

	// 生成并写入所有DATA帧
	offset := 0
	for offset < len(data) {
		chunkSize := len(data) - offset
		if chunkSize > maxPayload {
			chunkSize = maxPayload
		}
		chunk := data[offset : offset+chunkSize]

		// 构建DATA帧头部
		buf.WriteByte(0x00) // DATA帧类型
		buf.WriteByte(byte(chunkSize >> 8))
		buf.WriteByte(byte(chunkSize & 0xFF))
		// 写入数据载荷
		buf.Write(chunk)

		offset += chunkSize
	}

	// 将结果复制到缓冲区池中的切片，以便调用者可以归还
	result := GetBuffer(buf.Len())
	copy(result, buf.Bytes())
	return result[:buf.Len()], nil
}

// StreamObfuscate 流式混淆数据为HTTP/3格式
func (h *http3Obfuscator) StreamObfuscate(ctx context.Context, src io.Reader, dst io.Writer) (int64, error) {
	// 生成随机路径
	path := h.generateRandomPath()

	// 构造HTTP/3 HEADERS帧
	headersFrame := h.createHeadersFrame(path)
	defer PutBuffer(headersFrame)

	// 写入HEADERS帧
	if _, err := dst.Write(headersFrame); err != nil {
		return 0, fmt.Errorf("写入HEADERS帧失败: %w", err)
	}

	// 分片大小：32KB，与测试中的读取缓冲区对齐
	const maxPayload = 32768
	buf := GetBuffer(maxPayload)
	defer PutBuffer(buf)

	var totalWritten int64

	for {
		// 从源读取数据块
		n, err := io.ReadFull(src, buf[:maxPayload])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return totalWritten, fmt.Errorf("读取源数据失败: %w", err)
		}

		if n == 0 {
			// 没有更多数据
			break
		}

		chunk := buf[:n]

		// 构建DATA帧头部
		header := [3]byte{
			0x00, // DATA帧类型
			byte(n >> 8),
			byte(n & 0xFF),
		}

		// 写入帧头部
		if _, err := dst.Write(header[:]); err != nil {
			return totalWritten, fmt.Errorf("写入DATA帧头部失败: %w", err)
		}

		// 写入数据载荷（零拷贝直接写入）
		if _, err := dst.Write(chunk); err != nil {
			return totalWritten, fmt.Errorf("写入DATA帧载荷失败: %w", err)
		}

		totalWritten += int64(n)

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			// 已读取所有数据
			break
		}
	}

	return totalWritten, nil
}

// Deobfuscate 从HTTP/3格式解混淆数据
func (h *http3Obfuscator) Deobfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 解析HTTP/3帧序列：类型(1) + 长度(2) + 载荷
	var result []byte
	offset := 0

	for offset < len(data) {
		if len(data)-offset < 3 {
			// 不完整的帧头
			return nil, fmt.Errorf("不完整的帧头，剩余数据 %d 字节", len(data)-offset)
		}

		frameType := data[offset]
		length := uint16(data[offset+1])<<8 | uint16(data[offset+2])

		if len(data)-offset < 3+int(length) {
			// 不完整的帧载荷
			return nil, fmt.Errorf("不完整的帧载荷，需要 %d 字节，剩余 %d 字节",
				3+int(length), len(data)-offset)
		}

		payloadStart := offset + 3
		payloadEnd := payloadStart + int(length)

		// 只处理DATA帧（类型0x00）
		if frameType == 0x00 {
			result = append(result, data[payloadStart:payloadEnd]...)
		} else if frameType == 0x01 {
			// HEADERS帧，忽略
		} else {
			return nil, fmt.Errorf("未知帧类型: %02x", frameType)
		}

		offset = payloadEnd
	}

	if len(result) == 0 {
		return nil, fmt.Errorf("未找到DATA帧")
	}

	return result, nil
}

// StreamDeobfuscate 流式解混淆HTTP/3格式数据
func (h *http3Obfuscator) StreamDeobfuscate(ctx context.Context, src io.Reader, dst io.Writer) (int64, error) {
	// 使用带缓冲的读取器以提高效率
	reader := bufio.NewReader(src)
	var totalWritten int64

	for {
		// 读取帧头部（3字节）
		header, err := reader.Peek(3)
		if err == io.EOF {
			// 没有更多数据
			break
		}
		if err != nil {
			return totalWritten, fmt.Errorf("读取帧头部失败: %w", err)
		}

		frameType := header[0]
		length := uint16(header[1])<<8 | uint16(header[2])

		// 丢弃帧头部（Peek已读取，需要消耗）
		if _, err := reader.Discard(3); err != nil {
			return totalWritten, fmt.Errorf("丢弃帧头部失败: %w", err)
		}

		// 只处理DATA帧（类型0x00）
		if frameType == 0x00 {
			// 读取DATA帧载荷
			payload := GetBuffer(int(length))
			defer PutBuffer(payload)

			n, err := io.ReadFull(reader, payload[:length])
			if err != nil {
				return totalWritten, fmt.Errorf("读取DATA帧载荷失败: %w", err)
			}

			// 写入解混淆后的数据
			written, err := dst.Write(payload[:n])
			if err != nil {
				return totalWritten, fmt.Errorf("写入解混淆数据失败: %w", err)
			}
			totalWritten += int64(written)
		} else if frameType == 0x01 {
			// HEADERS帧，跳过载荷
			if _, err := reader.Discard(int(length)); err != nil {
				return totalWritten, fmt.Errorf("跳过HEADERS帧载荷失败: %w", err)
			}
		} else {
			return totalWritten, fmt.Errorf("未知帧类型: %02x", frameType)
		}
	}

	return totalWritten, nil
}

// GetMode 获取当前伪装形态
func (h *http3Obfuscator) GetMode() Mode {
	return ModeHTTP3
}

// SwitchMode 切换伪装形态
func (h *http3Obfuscator) SwitchMode(newMode Mode) error {
	// HTTP/3混淆器不支持切换形态
	return fmt.Errorf("HTTP/3混淆器不支持切换形态")
}

// generateRandomPath 生成随机路径
func (h *http3Obfuscator) generateRandomPath() string {
	template := h.httpConfig.PathTemplate
	if template == "" {
		template = "/api/v{version}/data/{id}"
	}

	// 替换占位符
	version := "1"
	id := generateRandomID(8)

	path := strings.ReplaceAll(template, "{version}", version)
	path = strings.ReplaceAll(path, "{id}", id)

	return path
}

// createHeadersFrame 创建HTTP/3 HEADERS帧
func (h *http3Obfuscator) createHeadersFrame(path string) []byte {
	// HTTP/3 HEADERS帧格式：类型(1) + 长度(2) + 载荷
	method := h.httpConfig.Method
	if method == "" {
		method = "GET"
	}

	host := h.httpConfig.HostHeader
	if host == "" {
		host = "example.com"
	}

	userAgent := h.httpConfig.UserAgent
	if userAgent == "" {
		userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	}

	// 构造HTTP头部
	headers := fmt.Sprintf(":method %s\r\n:path %s\r\n:authority %s\r\nuser-agent %s\r\n",
		method, path, host, userAgent)

	headersBytes := []byte(headers)
	frameSize := 3 + len(headersBytes)

	// 使用缓冲区池分配帧
	frame := GetBuffer(frameSize)
	frame[0] = 0x01 // HEADERS帧类型
	frame[1] = byte(len(headersBytes) >> 8)
	frame[2] = byte(len(headersBytes) & 0xFF)
	copy(frame[3:], headersBytes)

	// 返回切片（调用者负责归还）
	return frame[:frameSize]
}

// createDataFrame 创建HTTP/3 DATA帧
func (h *http3Obfuscator) createDataFrame(data []byte) []byte {
	// HTTP/3 DATA帧格式（简化版）
	// 类型(1) + 长度(2) + 数据
	frameSize := 3 + len(data)

	// 使用缓冲区池分配帧
	frame := GetBuffer(frameSize)
	frame[0] = 0x00 // DATA帧类型
	frame[1] = byte(len(data) >> 8)
	frame[2] = byte(len(data) & 0xFF)
	copy(frame[3:], data)

	// 返回切片（调用者负责归还）
	return frame[:frameSize]
}

// http3Frame HTTP/3帧结构
type http3Frame struct {
	Type     uint8
	Length   uint16
	Payload  []byte
	StreamID uint64
}

// parseHTTP3Frame 解析HTTP/3帧
func parseHTTP3Frame(data []byte) (*http3Frame, error) {
	if len(data) < 3 {
		return nil, fmt.Errorf("帧数据太短")
	}

	frame := &http3Frame{
		Type:   data[0],
		Length: uint16(data[1])<<8 | uint16(data[2]),
	}

	if len(data) < 3+int(frame.Length) {
		return nil, fmt.Errorf("帧长度不符")
	}

	frame.Payload = make([]byte, frame.Length)
	copy(frame.Payload, data[3:3+frame.Length])

	return frame, nil
}

// createWindowUpdateFrame 创建WINDOW_UPDATE帧
func (h *http3Transport) createWindowUpdateFrame(windowIncrement uint32) []byte {
	// 帧格式：类型(1) + 长度(2) + 窗口增量(4)
	const frameSize = 1 + 2 + 4 // 7字节
	frame := GetBuffer(frameSize)
	frame[0] = http3FrameWindowUpdate // 0x02
	frame[1] = 0                      // 长度高8位（4）
	frame[2] = 4                      // 长度低8位（4）
	binary.BigEndian.PutUint32(frame[3:], windowIncrement)
	return frame[:frameSize]
}

// handleWindowUpdateFrame 处理接收到的WINDOW_UPDATE帧
func (h *http3Transport) handleWindowUpdateFrame(payload []byte) error {
	if len(payload) != 4 {
		return fmt.Errorf("WINDOW_UPDATE帧载荷长度必须为4字节，实际为%d", len(payload))
	}
	windowIncrement := binary.BigEndian.Uint32(payload)
	oldWindow := h.windowSize.Load()
	// 增加发送窗口大小
	h.windowSize.Add(windowIncrement)
	newWindow := h.windowSize.Load()
	fmt.Printf("[HTTP3-WINDOW-UPDATE] 实例 %p 窗口更新: 增量=%d, 旧窗口=%d, 新窗口=%d\n",
		h, windowIncrement, oldWindow, newWindow)
	return nil
}

// processIncomingFrames 尝试从连接读取并处理传入的帧（特别是WINDOW_UPDATE帧）
func (h *http3Transport) processIncomingFrames() error {
	// 设置短暂的读取超时，避免阻塞
	if conn, ok := h.conn.(interface{ SetReadDeadline(time.Time) error }); ok {
		conn.SetReadDeadline(time.Now().Add(10 * time.Millisecond))
		defer conn.SetReadDeadline(time.Time{}) // 重置超时
	}
	// 尝试读取一些数据
	tmp := GetBuffer(4096)
	defer PutBuffer(tmp)
	n, err := h.conn.Read(tmp)
	if err != nil {
		// 忽略超时或暂时不可用错误
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			return nil
		}
		// 检查 BufferedPipe 的自定义超时错误
		if err.Error() == "操作超时" {
			return nil
		}
		return err
	}

	if n == 0 {
		return nil
	}
	// 将读取的数据追加到rawBuffer
	if cap(h.rawBuffer) < len(h.rawBuffer)+n {
		// 扩展缓冲区
		newSize := len(h.rawBuffer) + n
		if newSize < 65536 {
			newSize = 65536
		}
		newBuf := GetBuffer(newSize)
		copy(newBuf, h.rawBuffer)
		if len(h.rawBuffer) > 0 {
			PutBuffer(h.rawBuffer[:cap(h.rawBuffer)])
		}
		h.rawBuffer = newBuf[:len(h.rawBuffer)]
	}
	h.rawBuffer = append(h.rawBuffer, tmp[:n]...)

	// 扫描整个缓冲区，专门处理WINDOW_UPDATE帧，跳过DATA和HEADERS帧
	// 使用原地算法移除WINDOW_UPDATE帧，保留DATA/HEADERS帧供Read方法处理
	readIdx := 0
	writeIdx := 0
	for readIdx < len(h.rawBuffer) {
		if len(h.rawBuffer)-readIdx < 3 {
			// 剩余数据不足一个帧头，保留到后面
			break
		}
		frameType := h.rawBuffer[readIdx]
		length := uint16(h.rawBuffer[readIdx+1])<<8 | uint16(h.rawBuffer[readIdx+2])
		frameEnd := readIdx + 3 + int(length)
		if frameEnd > len(h.rawBuffer) {
			// 不完整的帧，等待更多数据
			break
		}

		if frameType == http3FrameWindowUpdate {
			// 处理窗口更新帧
			payload := h.rawBuffer[readIdx+3 : frameEnd]
			if err := h.handleWindowUpdateFrame(payload); err != nil {
				return err
			}
			// 跳过此帧（不复制到writeIdx位置），相当于移除
			readIdx = frameEnd
			// writeIdx保持不变，表示不保留此帧
		} else if frameType == http3FrameData || frameType == http3FrameHeaders {
			// DATA或HEADERS帧，保留在缓冲区中供Read方法处理
			// 将帧数据复制到writeIdx位置
			if writeIdx != readIdx {
				copy(h.rawBuffer[writeIdx:], h.rawBuffer[readIdx:frameEnd])
			}
			writeIdx += frameEnd - readIdx
			readIdx = frameEnd
		} else {
			// 未知帧类型，错误
			return fmt.Errorf("未知HTTP/3帧类型: %02x", frameType)
		}
	}

	// 复制剩余的不完整帧数据（如果有）
	if readIdx < len(h.rawBuffer) {
		remaining := len(h.rawBuffer) - readIdx
		if writeIdx != readIdx {
			copy(h.rawBuffer[writeIdx:], h.rawBuffer[readIdx:])
		}
		writeIdx += remaining
	}

	// 调整缓冲区大小
	h.rawBuffer = h.rawBuffer[:writeIdx]

	// 如果缓冲区变得很小，考虑缩减容量以节省内存
	if cap(h.rawBuffer) > 65536 && len(h.rawBuffer) < cap(h.rawBuffer)/4 {
		newBuf := GetBuffer(len(h.rawBuffer) * 2)
		copy(newBuf, h.rawBuffer)
		PutBuffer(h.rawBuffer[:cap(h.rawBuffer)])
		h.rawBuffer = newBuf[:len(h.rawBuffer)]
	}

	return nil
}

// writeDataFrameZeroCopy 使用零拷贝技术写入数据帧
func (h *http3Transport) writeDataFrameZeroCopy(data []byte) error {
	const maxFrameSize = 65535 // HTTP/3数据帧最大长度

	offset := 0
	for offset < len(data) {
		// 计算分片大小
		chunkSize := len(data) - offset
		if chunkSize > maxFrameSize {
			chunkSize = maxFrameSize
		}

		chunk := data[offset : offset+chunkSize]
		fmt.Printf("[HTTP3-WRITE-ZEROCOPY-CHUNK] 实例 %p 分片 %d-%d (大小=%d)\n", h, offset, offset+chunkSize, chunkSize)

		// 流量控制：检查发送窗口
		if err := h.waitForWindow(uint32(chunkSize)); err != nil {
			fmt.Printf("[HTTP3-WRITE-ZEROCOPY-WINDOW-ERROR] 实例 %p 窗口等待失败: %v\n", h, err)
			return err
		}

		// 构建帧头部（3字节）
		header := [3]byte{
			0x00, // DATA帧类型
			byte(chunkSize >> 8),
			byte(chunkSize & 0xFF),
		}

		// 使用MultiReader合并头部和数据（零拷贝）
		reader := io.MultiReader(
			bytes.NewReader(header[:]),
			bytes.NewReader(chunk),
		)

		// 使用缓冲区池中的临时缓冲区进行复制
		buf := GetBuffer(4096)
		defer PutBuffer(buf)
		fmt.Printf("[HTTP3-WRITE-ZEROCOPY-COPY] 实例 %p 开始复制分片到连接\n", h)
		n, err := io.CopyBuffer(h.conn, reader, buf)
		if err != nil {
			fmt.Printf("[HTTP3-WRITE-ZEROCOPY-COPY-ERROR] 实例 %p 复制失败: %v\n", h, err)
			return err
		}
		fmt.Printf("[HTTP3-WRITE-ZEROCOPY-COPY-SUCCESS] 实例 %p 复制 %d 字节\n", h, n)

		// 更新发送统计
		h.sentBytes.Add(uint32(chunkSize))
		offset += chunkSize
	}

	return nil
}

// waitForWindow 等待足够的发送窗口（零拷贝优化版本）
func (h *http3Transport) waitForWindow(needed uint32) error {
	const timeout = 30 * time.Second
	startTime := time.Now()

	for {
		sent := h.sentBytes.Load()
		window := h.windowSize.Load()
		available := window - sent

		fmt.Printf("[HTTP3-WAIT-WINDOW] 实例 %p 检查窗口: sent=%d, window=%d, available=%d, needed=%d\n",
			h, sent, window, available, needed)

		if needed <= available {
			fmt.Printf("[HTTP3-WAIT-WINDOW-SUFFICIENT] 实例 %p 窗口足够\n", h)
			return nil
		}

		if time.Since(startTime) > timeout {
			fmt.Printf("[HTTP3-WAIT-WINDOW-TIMEOUT] 实例 %p 等待窗口超时: sent=%d, window=%d, needed=%d\n",
				h, sent, window, needed)
			return fmt.Errorf("等待窗口超时: sent=%d, window=%d, needed=%d",
				sent, window, needed)
		}

		// 处理可能传入的窗口更新帧
		h.processIncomingFrames()

		// 使用更高效的等待策略：短暂休眠或使用条件变量
		time.Sleep(1 * time.Millisecond)

		if h.closed {
			return io.ErrClosedPipe
		}
	}
}

// streamWrite 使用流式混淆器写入数据
func (h *http3Transport) streamWrite(p []byte, isFirstWrite bool) (int, error) {
	if h.streamObfuscator == nil {
		return 0, fmt.Errorf("流式混淆器不可用")
	}

	fmt.Printf("[HTTP3-STREAM-WRITE] 实例 %p 开始流式写入 %d 字节，isFirstWrite=%v\n", h, len(p), isFirstWrite)

	// 创建字节读取器作为源
	src := bytes.NewReader(p)

	// 计算总发送大小（如果是第一次写入，需要包含HEADERS帧）
	totalSendSize := len(p)
	if isFirstWrite && h.headersFrame != nil {
		totalSendSize += len(h.headersFrame)
	}

	// 检查窗口是否足够
	if err := h.waitForWindow(uint32(totalSendSize)); err != nil {
		fmt.Printf("[HTTP3-STREAM-WRITE-WINDOW-ERROR] 实例 %p 窗口等待失败: %v\n", h, err)
		return 0, err
	}

	// 如果是第一次写入且需要发送HEADERS帧
	if isFirstWrite && h.headersFrame != nil {
		// 写入HEADERS帧
		buf := GetBuffer(4096)
		defer PutBuffer(buf)
		fmt.Printf("[HTTP3-STREAM-WRITE-HEADERS] 实例 %p 写入HEADERS帧，长度=%d\n", h, len(h.headersFrame))
		n, err := io.CopyBuffer(h.conn, bytes.NewReader(h.headersFrame), buf)
		if err != nil {
			fmt.Printf("[HTTP3-STREAM-WRITE-HEADERS-ERROR] 实例 %p HEADERS帧写入失败: %v\n", h, err)
			return 0, err
		}
		fmt.Printf("[HTTP3-STREAM-WRITE-HEADERS-SUCCESS] 实例 %p 写入 %d 字节\n", h, n)

		// 更新发送统计
		h.sentBytes.Add(uint32(n))
		h.hasSentHeaders = true
	}

	// 使用流式混淆器写入数据
	fmt.Printf("[HTTP3-STREAM-WRITE-DATA] 实例 %p 调用流式混淆器写入数据\n", h)
	written, err := h.streamObfuscator.StreamObfuscate(context.Background(), src, h.conn)
	if err != nil {
		fmt.Printf("[HTTP3-STREAM-WRITE-DATA-ERROR] 实例 %p 流式混淆器写入失败: %v\n", h, err)
		return 0, err
	}

	// 更新统计
	h.stats.BytesSent += uint64(written)
	h.sentBytes.Add(uint32(written))
	h.zeroCopyWriteCount.Add(1)
	fmt.Printf("[HTTP3-STREAM-WRITE-COMPLETE] 实例 %p 流式写入完成，写入 %d 字节\n", h, written)

	return int(written), nil
}
