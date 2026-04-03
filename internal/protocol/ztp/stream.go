// Package ztp 实现 Ztp 隧道协议
package ztp

import (
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// Stream 表示一个 Ztp 流
type Stream struct {
	// 标识
	id      uint32
	session *Session

	// 数据通道
	sendCh  chan []byte
	recvCh  chan []byte
	closeCh chan struct{}

	// 状态
	isClosed    atomic.Bool
	isLocalInit bool // 是否为本地发起

	// 超时
	writeTimeout time.Duration
	readTimeout  time.Duration
}

// ID 返回流ID
func (s *Stream) ID() uint32 {
	return s.id
}

// Write 写入数据到流
func (s *Stream) Write(data []byte) (n int, err error) {
	if s.isClosed.Load() {
		return 0, errors.New("流已关闭")
	}

	// 分帧发送（如果数据太大）
	totalWritten := 0
	for len(data) > 0 {
		chunkSize := len(data)
		if chunkSize > MaxFrameSize {
			chunkSize = MaxFrameSize
		}

		chunk := data[:chunkSize]
		data = data[chunkSize:]

		// 创建数据帧
		frame := NewFrame(TypeData, s.id, chunk)

		// 设置超时
		var timeoutCh <-chan time.Time
		if s.writeTimeout > 0 {
			timer := time.NewTimer(s.writeTimeout)
			defer timer.Stop()
			timeoutCh = timer.C
		}

		// 发送帧
		select {
		case s.session.sendCh <- frame:
			totalWritten += chunkSize
		case <-s.closeCh:
			return totalWritten, errors.New("流已关闭")
		case <-timeoutCh:
			return totalWritten, errors.New("写入超时")
		case <-s.session.closeCh:
			return totalWritten, errors.New("会话已关闭")
		}
	}

	return totalWritten, nil
}

// Read 从流读取数据
func (s *Stream) Read(p []byte) (n int, err error) {
	if s.isClosed.Load() {
		return 0, io.EOF
	}

	// 设置超时
	var timeoutCh <-chan time.Time
	if s.readTimeout > 0 {
		timer := time.NewTimer(s.readTimeout)
		defer timer.Stop()
		timeoutCh = timer.C
	}

	// 从接收通道读取数据
	select {
	case data := <-s.recvCh:
		if len(data) == 0 {
			// 空数据表示流结束
			return 0, io.EOF
		}

		// 复制数据到缓冲区
		copySize := len(data)
		if copySize > len(p) {
			copySize = len(p)
			// 剩余数据需要放回（简化处理，实际应缓存）
			// 这里简单处理，只返回部分数据
		}

		copy(p, data[:copySize])
		return copySize, nil

	case <-s.closeCh:
		return 0, io.EOF
	case <-timeoutCh:
		return 0, errors.New("读取超时")
	case <-s.session.closeCh:
		return 0, errors.New("会话已关闭")
	}
}

// Close 关闭流
func (s *Stream) Close() error {
	if s.isClosed.Load() {
		return nil
	}

	s.closeInternal()

	// 发送关闭流帧
	frame := NewFrame(TypeStreamClose, s.id, []byte("closed by local"))
	select {
	case s.session.sendCh <- frame:
	case <-s.session.closeCh:
		// 会话已关闭，忽略
	case <-time.After(5 * time.Second):
		// 发送超时，忽略
	}

	return nil
}

// closeInternal 内部关闭方法
func (s *Stream) closeInternal() {
	if s.isClosed.Load() {
		return
	}

	s.isClosed.Store(true)
	close(s.closeCh)

	// 从会话中移除
	s.session.streamsMu.Lock()
	delete(s.session.streams, s.id)
	s.session.streamsMu.Unlock()
}

// SetWriteTimeout 设置写超时
func (s *Stream) SetWriteTimeout(timeout time.Duration) {
	s.writeTimeout = timeout
}

// SetReadTimeout 设置读超时
func (s *Stream) SetReadTimeout(timeout time.Duration) {
	s.readTimeout = timeout
}

// IsClosed 检查流是否已关闭
func (s *Stream) IsClosed() bool {
	return s.isClosed.Load()
}

// LocalInit 是否为本地发起
func (s *Stream) LocalInit() bool {
	return s.isLocalInit
}

// Stream 实现 io.ReadWriteCloser 接口
var _ io.ReadWriteCloser = (*Stream)(nil)
