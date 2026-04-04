// Package ztp 实现 Ztp 隧道协议
package ztp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Session 表示一个 Ztp 会话，管理多个流
type Session struct {
	// 配置
	config SessionConfig

	// 底层传输
	transport io.ReadWriteCloser

	// 流管理
	streams    map[uint32]*Stream
	streamsMu  sync.RWMutex
	nextStream uint32 // 下一个可用的流ID（原子操作）

	// 帧发送和接收（优先级队列）
	highPriorityCh   chan *Frame // 高优先级帧
	normalPriorityCh chan *Frame // 普通优先级帧
	lowPriorityCh    chan *Frame // 低优先级帧
	recvCh           chan *Frame
	acceptCh         chan *Stream // 新流接受通道
	errors           chan error
	closeCh          chan struct{}
	closeOnce        sync.Once

	// 状态
	isClosed  atomic.Bool
	isStarted atomic.Bool

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// SessionConfig 会话配置
type SessionConfig struct {
	// MaxStreams 最大并发流数
	MaxStreams uint32

	// MaxFrameSize 最大帧大小
	MaxFrameSize uint32

	// SendBufferSize 发送缓冲区大小
	SendBufferSize int

	// ReceiveBufferSize 接收缓冲区大小
	ReceiveBufferSize int

	// IdleTimeout 空闲超时时间
	IdleTimeout time.Duration

	// PingInterval Ping间隔
	PingInterval time.Duration
}

// DefaultSessionConfig 返回默认会话配置
func DefaultSessionConfig() SessionConfig {
	return SessionConfig{
		MaxStreams:        65535,
		MaxFrameSize:      65535,
		SendBufferSize:    1024,
		ReceiveBufferSize: 1024,
		IdleTimeout:       60 * time.Second,
		PingInterval:      30 * time.Second,
	}
}

// NewSession 创建新会话
func NewSession(transport io.ReadWriteCloser, config SessionConfig) (*Session, error) {
	if transport == nil {
		return nil, errors.New("传输层不能为nil")
	}

	if config.MaxStreams == 0 {
		config.MaxStreams = 65535
	}
	if config.MaxFrameSize == 0 {
		config.MaxFrameSize = 65535
	}
	if config.SendBufferSize == 0 {
		config.SendBufferSize = 1024
	}
	if config.ReceiveBufferSize == 0 {
		config.ReceiveBufferSize = 1024
	}

	ctx, cancel := context.WithCancel(context.Background())

	s := &Session{
		config:           config,
		transport:        transport,
		streams:          make(map[uint32]*Stream),
		nextStream:       1, // 客户端发起的流使用奇数ID
		highPriorityCh:   make(chan *Frame, config.SendBufferSize),
		normalPriorityCh: make(chan *Frame, config.SendBufferSize),
		lowPriorityCh:    make(chan *Frame, config.SendBufferSize),
		recvCh:           make(chan *Frame, config.ReceiveBufferSize),
		acceptCh:         make(chan *Stream, config.ReceiveBufferSize), // 缓冲大小与recvCh相同
		errors:           make(chan error, 10),
		closeCh:          make(chan struct{}),
		ctx:              ctx,
		cancel:           cancel,
	}

	return s, nil
}

// Start 启动会话
func (s *Session) Start() error {
	if s.isStarted.Load() {
		return errors.New("会话已经启动")
	}

	s.isStarted.Store(true)

	// 启动发送协程
	s.wg.Add(1)
	go s.sendLoop()

	// 启动接收协程
	s.wg.Add(1)
	go s.recvLoop()

	// 启动处理协程
	s.wg.Add(1)
	go s.processLoop()

	// 启动Ping协程（如果需要）
	if s.config.PingInterval > 0 {
		s.wg.Add(1)
		go s.pingLoop()
	}

	fmt.Println("Ztp会话已启动")
	return nil
}

// Close 关闭会话
func (s *Session) Close() error {
	if s.isClosed.Load() {
		return nil
	}

	s.closeOnce.Do(func() {
		s.isClosed.Store(true)
		s.cancel()

		// 关闭所有流
		s.streamsMu.Lock()
		for _, stream := range s.streams {
			stream.closeInternal(false) // 会话将替换映射，不需要逐个删除
		}
		s.streams = make(map[uint32]*Stream)
		s.streamsMu.Unlock()

		// 关闭通道
		close(s.closeCh)

		// 等待所有协程退出
		s.wg.Wait()

		// 关闭底层传输
		s.transport.Close()

		fmt.Println("Ztp会话已关闭")
	})

	return nil
}

// sendFrame 发送帧到对应的优先级通道
func (s *Session) sendFrame(frame *Frame) error {
	// 根据帧的优先级选择通道
	var ch chan *Frame

	if frame.HasFlag(FlagPriority) {
		// 高优先级
		ch = s.highPriorityCh
	} else if frame.HasFlag(FlagLowPriority) {
		// 低优先级
		ch = s.lowPriorityCh
	} else {
		// 普通优先级
		ch = s.normalPriorityCh
	}

	// 发送帧，支持超时和取消
	select {
	case ch <- frame:
		return nil
	case <-s.closeCh:
		return errors.New("会话已关闭")
	case <-time.After(5 * time.Second):
		return errors.New("发送帧超时")
	}
}

// trySendFrame 尝试发送帧，非阻塞，失败返回false
func (s *Session) trySendFrame(frame *Frame) bool {
	// 根据帧的优先级选择通道
	var ch chan *Frame

	if frame.HasFlag(FlagPriority) {
		// 高优先级
		ch = s.highPriorityCh
	} else if frame.HasFlag(FlagLowPriority) {
		// 低优先级
		ch = s.lowPriorityCh
	} else {
		// 普通优先级
		ch = s.normalPriorityCh
	}

	// 非阻塞发送
	select {
	case ch <- frame:
		return true
	default:
		return false
	}
}

// OpenStream 打开新流
func (s *Session) OpenStream() (*Stream, error) {
	if s.isClosed.Load() {
		return nil, errors.New("会话已关闭")
	}

	// 分配流ID（客户端发起使用奇数ID）
	streamID := atomic.AddUint32(&s.nextStream, 2) - 2
	if streamID > s.config.MaxStreams {
		return nil, errors.New("达到最大流数限制")
	}

	// 创建流
	stream := &Stream{
		id:           streamID,
		session:      s,
		recvCh:       make(chan []byte, s.config.ReceiveBufferSize),
		closeCh:      make(chan struct{}),
		isClosed:     atomic.Bool{},
		isLocalInit:  true,
		writeTimeout: 30 * time.Second,
		readTimeout:  30 * time.Second,
	}
	// 初始化流控窗口（初始窗口大小 = 65535字节）
	stream.windowSize.Store(65535)

	// 注册流
	s.streamsMu.Lock()
	s.streams[streamID] = stream
	s.streamsMu.Unlock()

	// 发送打开流帧
	frame := NewFrame(TypeStreamOpen, streamID, nil)
	if err := s.sendFrame(frame); err != nil {
		return nil, err
	}

	return stream, nil
}

// AcceptStream 接受远程打开的流，阻塞直到有流可接受或上下文取消
func (s *Session) AcceptStream(ctx context.Context) (*Stream, error) {
	select {
	case stream := <-s.acceptCh:
		return stream, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.closeCh:
		return nil, errors.New("会话已关闭")
	}
}

// sendLoop 发送循环
func (s *Session) sendLoop() {
	defer s.wg.Done()

	for {
		// 优先级选择：先检查高优先级，然后普通，最后低优先级
		var frame *Frame
		select {
		case frame = <-s.highPriorityCh:
			// 高优先级帧
		default:
			select {
			case frame = <-s.normalPriorityCh:
				// 普通优先级帧
			default:
				select {
				case frame = <-s.lowPriorityCh:
					// 低优先级帧
				default:
					// 所有通道都为空，阻塞等待任一通道有数据
					select {
					case frame = <-s.highPriorityCh:
					case frame = <-s.normalPriorityCh:
					case frame = <-s.lowPriorityCh:
					case <-s.ctx.Done():
						return
					case <-s.closeCh:
						return
					}
				}
			}
		}

		if frame == nil {
			// 没有帧但被唤醒，可能是上下文取消
			continue
		}

		// 发送帧
		if err := WriteFrame(s.transport, frame); err != nil {
			s.errors <- fmt.Errorf("发送帧失败: %w", err)
			return
		}
	}
}

// recvLoop 接收循环
func (s *Session) recvLoop() {
	defer s.wg.Done()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.closeCh:
			return
		default:
			// 设置读超时
			if conn, ok := s.transport.(interface{ SetReadDeadline(time.Time) error }); ok {
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			}

			frame, err := ReadFrame(s.transport)
			if err != nil {
				if errors.Is(err, io.EOF) {
					s.errors <- errors.New("连接已关闭")
					return
				}
				// 超时错误继续循环
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					continue
				}
				s.errors <- fmt.Errorf("读取帧失败: %w", err)
				return
			}

			// 将帧发送到处理通道
			select {
			case s.recvCh <- frame:
			case <-s.ctx.Done():
				return
			case <-s.closeCh:
				return
			}
		}
	}
}

// processLoop 处理循环
func (s *Session) processLoop() {
	defer s.wg.Done()

	for {
		select {
		case frame := <-s.recvCh:
			s.handleFrame(frame)
		case err := <-s.errors:
			fmt.Printf("会话错误: %v\n", err)
			s.Close()
			return
		case <-s.ctx.Done():
			return
		case <-s.closeCh:
			return
		}
	}
}

// handleFrame 处理接收到的帧
func (s *Session) handleFrame(frame *Frame) {
	switch frame.Type {
	case TypeData:
		s.handleDataFrame(frame)
	case TypeStreamOpen:
		s.handleStreamOpenFrame(frame)
	case TypeStreamClose:
		s.handleStreamCloseFrame(frame)
	case TypeAck:
		s.handleAckFrame(frame)
	case TypePing:
		s.handlePingFrame(frame)
	case TypeReset:
		s.handleResetFrame(frame)
	default:
		s.errors <- fmt.Errorf("未知帧类型: %#x", frame.Type)
	}
}

// handleDataFrame 处理数据帧
func (s *Session) handleDataFrame(frame *Frame) {
	s.streamsMu.RLock()
	stream, ok := s.streams[frame.StreamID]
	s.streamsMu.RUnlock()

	if !ok {
		// 流不存在，发送重置帧
		resetFrame := NewFrame(TypeReset, frame.StreamID, []byte("stream not found"))
		// 尝试发送重置帧（普通优先级）
		s.trySendFrame(resetFrame)
		return
	}

	// 将数据传递给流
	select {
	case stream.recvCh <- frame.Payload:
		// 数据成功接收，更新接收字节数并发送ACK
		newReceived := stream.receivedBytes.Add(uint32(len(frame.Payload)))

		// 计算接收窗口（简化：固定窗口大小，未来可根据缓冲区可用空间调整）
		receiveWindow := uint32(65535) // 固定接收窗口

		// 发送ACK帧（高优先级）
		ackFrame := NewAckFrame(frame.StreamID, newReceived, receiveWindow)
		s.trySendFrame(ackFrame)
	case <-stream.closeCh:
		// 流已关闭，忽略数据
	case <-time.After(100 * time.Millisecond):
		// 接收缓冲区满，可能流处理慢
		// 注意：这里没有发送ACK，发送方会等待
	}
}

// handleStreamOpenFrame 处理打开流帧
func (s *Session) handleStreamOpenFrame(frame *Frame) {
	// 检查流ID是否已存在
	s.streamsMu.RLock()
	_, exists := s.streams[frame.StreamID]
	s.streamsMu.RUnlock()

	if exists {
		// 发送重置帧（普通优先级）
		resetFrame := NewFrame(TypeReset, frame.StreamID, []byte("stream already exists"))
		s.trySendFrame(resetFrame)
		return
	}

	// 创建流（远程发起）
	stream := &Stream{
		id:           frame.StreamID,
		session:      s,
		recvCh:       make(chan []byte, s.config.ReceiveBufferSize),
		closeCh:      make(chan struct{}),
		isClosed:     atomic.Bool{},
		isLocalInit:  false,
		writeTimeout: 30 * time.Second,
		readTimeout:  30 * time.Second,
	}
	// 初始化流控窗口（初始窗口大小 = 65535字节）
	stream.windowSize.Store(65535)

	// 注册流
	s.streamsMu.Lock()
	s.streams[frame.StreamID] = stream
	s.streamsMu.Unlock()

	// 通知有新的流可接受
	select {
	case s.acceptCh <- stream:
		// 成功通知
	default:
		// 接受通道已满，无法通知，流仍然存在但用户无法获取
		// 可以选择关闭流或忽略
	}
}

// handleStreamCloseFrame 处理关闭流帧
func (s *Session) handleStreamCloseFrame(frame *Frame) {
	s.streamsMu.Lock()
	stream, ok := s.streams[frame.StreamID]
	if ok {
		stream.closeInternal(false) // 会话已持有锁，稍后会删除
		delete(s.streams, frame.StreamID)
	}
	s.streamsMu.Unlock()
}

// handleAckFrame 处理确认帧
func (s *Session) handleAckFrame(frame *Frame) {
	// 解码ACK载荷
	ack, err := DecodeAckPayload(frame.Payload)
	if err != nil {
		s.errors <- fmt.Errorf("解码ACK载荷失败: %w", err)
		return
	}

	// 查找对应的流
	s.streamsMu.RLock()
	stream, exists := s.streams[frame.StreamID]
	s.streamsMu.RUnlock()

	if !exists {
		// 流不存在，忽略ACK
		return
	}

	// 更新流的确认字节数和窗口大小
	// 注意：ackedBytes应该是单调递增的，只接受更大的确认值
	currentAcked := stream.ackedBytes.Load()
	if ack.AckedBytes > currentAcked {
		stream.ackedBytes.Store(ack.AckedBytes)
	}

	// 更新窗口大小
	stream.windowSize.Store(ack.WindowSize)

	// 调试日志
	fmt.Printf("流 %d ACK更新: acked=%d->%d, window=%d\n",
		frame.StreamID, currentAcked, ack.AckedBytes, ack.WindowSize)
}

// handlePingFrame 处理Ping帧
func (s *Session) handlePingFrame(frame *Frame) {
	// 发送Pong响应（使用ACK帧，普通优先级）
	ackFrame := NewFrame(TypeAck, frame.StreamID, []byte("pong"))
	s.trySendFrame(ackFrame)
}

// handleResetFrame 处理重置帧
func (s *Session) handleResetFrame(frame *Frame) {
	s.streamsMu.Lock()
	stream, ok := s.streams[frame.StreamID]
	if ok {
		stream.closeInternal(false) // 会话已持有锁，稍后会删除
		delete(s.streams, frame.StreamID)
	}
	s.streamsMu.Unlock()
}

// pingLoop Ping循环
func (s *Session) pingLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.PingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if s.isClosed.Load() {
				return
			}

			// 发送Ping帧（使用流ID 0，低优先级）
			pingFrame := NewFrame(TypePing, 0, []byte("ping"))
			pingFrame.SetFlag(FlagLowPriority)
			if err := s.sendFrame(pingFrame); err != nil {
				// 发送失败，会话可能已关闭
				return
			}
		case <-s.ctx.Done():
			return
		case <-s.closeCh:
			return
		}
	}
}

// StreamCount 返回当前流数量
func (s *Session) StreamCount() int {
	s.streamsMu.RLock()
	defer s.streamsMu.RUnlock()
	return len(s.streams)
}

// IsClosed 检查会话是否已关闭
func (s *Session) IsClosed() bool {
	return s.isClosed.Load()
}
