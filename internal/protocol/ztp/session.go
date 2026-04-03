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

	// 帧发送和接收
	sendCh    chan *Frame
	recvCh    chan *Frame
	errors    chan error
	closeCh   chan struct{}
	closeOnce sync.Once

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
		config:     config,
		transport:  transport,
		streams:    make(map[uint32]*Stream),
		nextStream: 1, // 客户端发起的流使用奇数ID
		sendCh:     make(chan *Frame, config.SendBufferSize),
		recvCh:     make(chan *Frame, config.ReceiveBufferSize),
		errors:     make(chan error, 10),
		closeCh:    make(chan struct{}),
		ctx:        ctx,
		cancel:     cancel,
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
			stream.closeInternal()
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
		sendCh:       make(chan []byte, s.config.SendBufferSize),
		recvCh:       make(chan []byte, s.config.ReceiveBufferSize),
		closeCh:      make(chan struct{}),
		isClosed:     atomic.Bool{},
		isLocalInit:  true,
		writeTimeout: 30 * time.Second,
		readTimeout:  30 * time.Second,
	}

	// 注册流
	s.streamsMu.Lock()
	s.streams[streamID] = stream
	s.streamsMu.Unlock()

	// 发送打开流帧
	frame := NewFrame(TypeStreamOpen, streamID, nil)
	select {
	case s.sendCh <- frame:
	case <-s.closeCh:
		return nil, errors.New("会话已关闭")
	case <-time.After(5 * time.Second):
		return nil, errors.New("发送打开流帧超时")
	}

	return stream, nil
}

// AcceptStream 接受远程打开的流
func (s *Session) AcceptStream() (*Stream, error) {
	// TODO: 实现流接受逻辑
	// 这需要从接收队列中获取STREAM_OPEN帧
	return nil, errors.New("尚未实现")
}

// sendLoop 发送循环
func (s *Session) sendLoop() {
	defer s.wg.Done()

	for {
		select {
		case frame := <-s.sendCh:
			if err := WriteFrame(s.transport, frame); err != nil {
				s.errors <- fmt.Errorf("发送帧失败: %w", err)
				return
			}
		case <-s.ctx.Done():
			return
		case <-s.closeCh:
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
		select {
		case s.sendCh <- resetFrame:
		default:
			// 发送缓冲区满，忽略
		}
		return
	}

	// 将数据传递给流
	select {
	case stream.recvCh <- frame.Payload:
	case <-stream.closeCh:
		// 流已关闭，忽略数据
	case <-time.After(100 * time.Millisecond):
		// 接收缓冲区满，可能流处理慢
	}
}

// handleStreamOpenFrame 处理打开流帧
func (s *Session) handleStreamOpenFrame(frame *Frame) {
	// 检查流ID是否已存在
	s.streamsMu.RLock()
	_, exists := s.streams[frame.StreamID]
	s.streamsMu.RUnlock()

	if exists {
		// 发送重置帧
		resetFrame := NewFrame(TypeReset, frame.StreamID, []byte("stream already exists"))
		select {
		case s.sendCh <- resetFrame:
		default:
		}
		return
	}

	// 创建流（远程发起）
	stream := &Stream{
		id:           frame.StreamID,
		session:      s,
		sendCh:       make(chan []byte, s.config.SendBufferSize),
		recvCh:       make(chan []byte, s.config.ReceiveBufferSize),
		closeCh:      make(chan struct{}),
		isClosed:     atomic.Bool{},
		isLocalInit:  false,
		writeTimeout: 30 * time.Second,
		readTimeout:  30 * time.Second,
	}

	// 注册流
	s.streamsMu.Lock()
	s.streams[frame.StreamID] = stream
	s.streamsMu.Unlock()

	// 通知有新的流可接受（TODO: 实现AcceptStream机制）
}

// handleStreamCloseFrame 处理关闭流帧
func (s *Session) handleStreamCloseFrame(frame *Frame) {
	s.streamsMu.Lock()
	stream, ok := s.streams[frame.StreamID]
	if ok {
		stream.closeInternal()
		delete(s.streams, frame.StreamID)
	}
	s.streamsMu.Unlock()
}

// handleAckFrame 处理确认帧
func (s *Session) handleAckFrame(frame *Frame) {
	// TODO: 实现ACK处理
}

// handlePingFrame 处理Ping帧
func (s *Session) handlePingFrame(frame *Frame) {
	// 发送Pong响应（使用ACK帧）
	ackFrame := NewFrame(TypeAck, frame.StreamID, []byte("pong"))
	select {
	case s.sendCh <- ackFrame:
	default:
	}
}

// handleResetFrame 处理重置帧
func (s *Session) handleResetFrame(frame *Frame) {
	s.streamsMu.Lock()
	stream, ok := s.streams[frame.StreamID]
	if ok {
		stream.closeInternal()
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

			// 发送Ping帧（使用流ID 0）
			pingFrame := NewFrame(TypePing, 0, []byte("ping"))
			select {
			case s.sendCh <- pingFrame:
			case <-s.closeCh:
				return
			case <-s.ctx.Done():
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
