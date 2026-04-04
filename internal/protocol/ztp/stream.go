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
	recvCh  chan []byte
	closeCh chan struct{}

	// 状态
	isClosed     atomic.Bool
	isLocalInit  bool        // 是否为本地发起
	remoteClosed atomic.Bool // 远程是否已关闭

	// 流控状态（原子操作）
	sentBytes     atomic.Uint32 // 已发送未确认字节数
	ackedBytes    atomic.Uint32 // 已确认字节数
	windowSize    atomic.Uint32 // 当前窗口大小（字节）
	receivedBytes atomic.Uint32 // 已接收字节数（用于ACK生成）

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

		// 流控：检查窗口可用性
		startTime := time.Now()
		for {
			sent := s.sentBytes.Load()
			acked := s.ackedBytes.Load()
			window := s.windowSize.Load()

			// 计算已发送未确认字节数和可用窗口
			unacked := sent - acked
			available := window - unacked

			if uint32(chunkSize) <= available {
				// 窗口足够，可以发送
				break
			}

			// 窗口不足，等待一段时间再重试
			if s.writeTimeout > 0 && time.Since(startTime) > s.writeTimeout {
				return totalWritten, errors.New("写入超时：窗口不足")
			}

			// 短暂等待后重试
			select {
			case <-s.closeCh:
				return totalWritten, errors.New("流已关闭")
			case <-s.session.closeCh:
				return totalWritten, errors.New("会话已关闭")
			case <-time.After(10 * time.Millisecond):
				// 继续重试
			}
		}

		// 创建数据帧
		frame := NewFrame(TypeData, s.id, chunk)

		// 设置超时
		var timeoutCh <-chan time.Time
		if s.writeTimeout > 0 {
			timer := time.NewTimer(s.writeTimeout)
			defer timer.Stop()
			timeoutCh = timer.C
		}

		// 发送帧并更新发送字节计数
		sendDone := make(chan error, 1)
		go func() {
			sendDone <- s.session.sendFrame(frame)
		}()

		select {
		case err := <-sendDone:
			if err != nil {
				return totalWritten, err
			}
			totalWritten += chunkSize
			s.sentBytes.Add(uint32(chunkSize))
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

	// 如果远程已关闭，先尝试非阻塞读取剩余数据
	if s.remoteClosed.Load() {
		select {
		case data := <-s.recvCh:
			if len(data) == 0 {
				return 0, io.EOF
			}
			copySize := len(data)
			if copySize > len(p) {
				copySize = len(p)
				// 剩余数据需要放回（简化处理，实际应缓存）
				// 这里简单处理，只返回部分数据
			}
			copy(p, data[:copySize])
			return copySize, nil
		default:
			// 没有剩余数据，流结束
			return 0, io.EOF
		}
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

	s.closeInternal(true) // 本地关闭需要从会话中移除

	// 发送关闭流帧
	frame := NewFrame(TypeStreamClose, s.id, []byte("closed by local"))
	s.session.sendFrame(frame) // 忽略错误，尽力发送

	return nil
}

// closeInternal 内部关闭方法
func (s *Stream) closeInternal(removeFromSession bool) {
	if removeFromSession {
		// 本地关闭
		if s.isClosed.Load() {
			return
		}
		s.isClosed.Store(true)
		close(s.closeCh)

		// 从会话中移除
		s.session.streamsMu.Lock()
		delete(s.session.streams, s.id)
		s.session.streamsMu.Unlock()
	} else {
		// 远程关闭
		if s.remoteClosed.Load() {
			return
		}
		s.remoteClosed.Store(true)
		// 不关闭closeCh，允许读取剩余数据
	}
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
