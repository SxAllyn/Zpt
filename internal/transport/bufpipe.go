// Package transport 提供传输层实现
package transport

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// BufferedPipe 是带缓冲的内存管道，实现 net.Conn 接口
// 用于替代 net.Pipe() 以解决同步阻塞导致的死锁问题
// 使用简单字节切片缓冲区设计，非环形缓冲区，简化实现
type BufferedPipe struct {
	// 配置参数
	bufferSize int // 最大缓冲区大小（字节）
	maxSize    int // 单个写入操作最大大小

	// 缓冲区状态
	mu     sync.RWMutex // 保护缓冲区的互斥锁
	buffer []byte       // 数据缓冲区
	closed bool         // 管道是否已关闭

	// 对端管道（用于双向通信）
	peer *BufferedPipe // 指向对端管道

	// 同步原语
	readCond  *sync.Cond // 读取条件变量（缓冲区非空时唤醒）
	writeCond *sync.Cond // 写入条件变量（缓冲区有空间时唤醒）

	// 超时支持
	readDeadline  time.Time // 读取截止时间
	writeDeadline time.Time // 写入截止时间

	// 连接信息（用于实现 net.Conn 接口）
	localAddr  net.Addr
	remoteAddr net.Addr
}

// 错误定义
var (
	ErrBufferFull  = errors.New("缓冲区已满")
	ErrBufferEmpty = errors.New("缓冲区为空")
	ErrPipeClosed  = errors.New("管道已关闭")
	ErrDeadline    = errors.New("操作超时")
)

// NewBufferedPipe 创建一对连接的带缓冲管道
// bufferSize: 缓冲区大小（字节），默认 1MB
func NewBufferedPipe(bufferSize int) (net.Conn, net.Conn) {
	if bufferSize <= 0 {
		bufferSize = 10 * 1024 * 1024 // 默认 10MB
	}

	// 创建两个独立的管道，方向相反
	pipe1 := newBufferedPipe(bufferSize)
	pipe2 := newBufferedPipe(bufferSize)

	// 设置对端引用
	pipe1.peer = pipe2
	pipe2.peer = pipe1

	// 交叉连接：pipe1 的写入是 pipe2 的读取，反之亦然
	pipe1.remoteAddr = pipe2.localAddr
	pipe2.remoteAddr = pipe1.localAddr

	fmt.Printf("[PIPE] 创建管道对: pipe1=%p, pipe2=%p, bufferSize=%d\n", pipe1, pipe2, bufferSize)

	return pipe1, pipe2
}

// newBufferedPipe 创建单个方向的带缓冲管道
func newBufferedPipe(bufferSize int) *BufferedPipe {
	pipe := &BufferedPipe{
		bufferSize: bufferSize,
		maxSize:    bufferSize / 4, // 单个写入操作最大为缓冲区的 1/4
		buffer:     make([]byte, 0, bufferSize),
		closed:     false,
	}

	// 初始化条件变量
	pipe.readCond = sync.NewCond(&pipe.mu)
	pipe.writeCond = sync.NewCond(&pipe.mu)

	// 设置本地地址（虚拟地址）
	pipe.localAddr = &net.TCPAddr{
		IP:   net.IPv4(127, 0, 0, 1),
		Port: 0,
	}

	// 初始远程地址为 nil，将在 NewBufferedPipe 中设置
	pipe.remoteAddr = nil

	return pipe
}

// Read 从管道读取数据
func (p *BufferedPipe) Read(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 等待数据可读或超时
	for len(p.buffer) == 0 && !p.closed {
		if !p.readDeadline.IsZero() && time.Now().After(p.readDeadline) {
			return 0, ErrDeadline
		}

		// 如果有截止时间，使用带超时的等待
		if !p.readDeadline.IsZero() {
			now := time.Now()
			if now.After(p.readDeadline) {
				return 0, ErrDeadline
			}
			timeout := p.readDeadline.Sub(now)

			// 使用条件变量超时等待
			timer := time.AfterFunc(timeout, func() {
				p.mu.Lock()
				p.readCond.Broadcast()
				p.mu.Unlock()
			})

			p.readCond.Wait()
			timer.Stop()
		} else {
			p.readCond.Wait()
		}
	}

	// 检查管道是否已关闭
	if p.closed && len(p.buffer) == 0 {
		return 0, io.EOF
	}

	// 从缓冲区读取数据
	n = copy(b, p.buffer)
	p.buffer = p.buffer[n:] // 移除已读取的数据

	// 通知写入方有空间了
	p.writeCond.Signal()

	return n, nil
}

// legacyWrite 旧版本写入实现（写入自己的缓冲区）
func (p *BufferedPipe) legacyWrite(b []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 检查管道是否已关闭
	if p.closed {
		return 0, ErrPipeClosed
	}

	totalWritten := 0
	for totalWritten < len(b) {
		// 计算可用空间
		available := p.bufferSize - len(p.buffer)
		if available == 0 {
			// 缓冲区已满，等待空间或超时
			if !p.writeDeadline.IsZero() && time.Now().After(p.writeDeadline) {
				return totalWritten, ErrDeadline
			}

			// 如果有截止时间，使用带超时的等待
			if !p.writeDeadline.IsZero() {
				now := time.Now()
				if now.After(p.writeDeadline) {
					return totalWritten, ErrDeadline
				}
				timeout := p.writeDeadline.Sub(now)

				// 使用条件变量超时等待
				timer := time.AfterFunc(timeout, func() {
					p.mu.Lock()
					p.writeCond.Broadcast()
					p.mu.Unlock()
				})

				p.writeCond.Wait()
				timer.Stop()
			} else {
				p.writeCond.Wait()
			}

			// 再次检查管道状态
			if p.closed {
				return totalWritten, ErrPipeClosed
			}

			// 重新计算可用空间
			available = p.bufferSize - len(p.buffer)
		}

		// 写入数据到缓冲区
		chunk := b[totalWritten:]
		if len(chunk) > available {
			chunk = chunk[:available]
		}
		if len(chunk) > p.maxSize {
			chunk = chunk[:p.maxSize]
		}

		p.buffer = append(p.buffer, chunk...)
		totalWritten += len(chunk)

		// 通知读取方有新数据
		p.readCond.Signal()
	}

	return totalWritten, nil
}

// Write 向管道写入数据（写入对端管道的缓冲区）
func (p *BufferedPipe) Write(b []byte) (n int, err error) {
	if len(b) == 0 {
		return 0, nil
	}

	// 获取对端管道
	peer := p.peer
	if peer == nil {
		// 如果没有对端，使用旧的行为（仅用于测试）
		return p.legacyWrite(b)
	}

	// 锁定对端管道（接收方）
	peer.mu.Lock()
	defer peer.mu.Unlock()

	fmt.Printf("[PIPE-WRITE-START] 管道 %p -> 对端 %p，写入 %d 字节，对端缓冲区=%d/%d，对端关闭=%v\n",
		p, peer, len(b), len(peer.buffer), peer.bufferSize, peer.closed)

	// 检查对端管道是否已关闭（接收方关闭）
	if peer.closed {
		fmt.Printf("[PIPE-WRITE-ERROR] 管道 %p 写入失败：对端管道 %p 已关闭\n", p, peer)
		return 0, ErrPipeClosed
	}

	totalWritten := 0
	for totalWritten < len(b) {
		// 计算对端缓冲区的可用空间
		available := peer.bufferSize - len(peer.buffer)
		fmt.Printf("[PIPE-WRITE-STATUS] 管道 %p 写入循环，已写入 %d/%d，对端可用空间=%d，对端缓冲区=%d/%d\n",
			p, totalWritten, len(b), available, len(peer.buffer), peer.bufferSize)
		if available == 0 {
			// 对端缓冲区已满，等待空间或超时
			if !p.writeDeadline.IsZero() && time.Now().After(p.writeDeadline) {
				fmt.Printf("[PIPE-WRITE] 写入超时，已写入 %d/%d 字节\n", totalWritten, len(b))
				return totalWritten, ErrDeadline
			}

			// 如果有截止时间，使用带超时的等待
			if !p.writeDeadline.IsZero() {
				now := time.Now()
				if now.After(p.writeDeadline) {
					return totalWritten, ErrDeadline
				}
				timeout := p.writeDeadline.Sub(now)

				// 使用条件变量超时等待
				timer := time.AfterFunc(timeout, func() {
					peer.mu.Lock()
					peer.writeCond.Broadcast()
					peer.mu.Unlock()
				})

				// 等待对端缓冲区的写入条件（空间可用）
				peer.writeCond.Wait()
				timer.Stop()
			} else {
				peer.writeCond.Wait()
			}

			// 再次检查对端管道状态
			if peer.closed {
				fmt.Printf("[PIPE-WRITE-ERROR] 管道 %p 写入失败（等待后）：对端管道 %p 已关闭，已写入 %d/%d 字节\n", p, peer, totalWritten, len(b))
				return totalWritten, ErrPipeClosed
			}

			// 重新计算可用空间
			available = peer.bufferSize - len(peer.buffer)
		}

		// 写入数据到对端缓冲区
		chunk := b[totalWritten:]
		if len(chunk) > available {
			chunk = chunk[:available]
		}
		if len(chunk) > p.maxSize {
			chunk = chunk[:p.maxSize]
		}

		peer.buffer = append(peer.buffer, chunk...)
		totalWritten += len(chunk)
		fmt.Printf("[PIPE-WRITE-CHUNK] 管道 %p 写入 %d 字节到对端，累计写入 %d/%d，对端缓冲区=%d/%d\n",
			p, len(chunk), totalWritten, len(b), len(peer.buffer), peer.bufferSize)

		// 通知对端管道的读取方有新数据（唤醒等待读取的goroutine）
		peer.readCond.Signal()
	}

	fmt.Printf("[PIPE-WRITE-END] 管道 %p 写入完成，总计写入 %d/%d 字节\n", p, totalWritten, len(b))
	return totalWritten, nil
}

// Close 关闭管道
func (p *BufferedPipe) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return nil
	}

	fmt.Printf("[PIPE-CLOSE] 管道 %p 关闭，本地地址 %s，远程地址 %s\n", p, p.localAddr, p.remoteAddr)
	p.closed = true

	// 唤醒所有等待的 goroutine
	p.readCond.Broadcast()
	p.writeCond.Broadcast()

	return nil
}

// LocalAddr 返回本地地址
func (p *BufferedPipe) LocalAddr() net.Addr {
	return p.localAddr
}

// RemoteAddr 返回远程地址
func (p *BufferedPipe) RemoteAddr() net.Addr {
	return p.remoteAddr
}

// SetDeadline 设置读写截止时间
func (p *BufferedPipe) SetDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.readDeadline = t
	p.writeDeadline = t

	// 唤醒等待的 goroutine 以检查新截止时间
	p.readCond.Broadcast()
	p.writeCond.Broadcast()

	return nil
}

// SetReadDeadline 设置读取截止时间
func (p *BufferedPipe) SetReadDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.readDeadline = t
	p.readCond.Broadcast()

	return nil
}

// SetWriteDeadline 设置写入截止时间
func (p *BufferedPipe) SetWriteDeadline(t time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.writeDeadline = t
	p.writeCond.Broadcast()

	return nil
}

// TryRead 尝试非阻塞读取
func (p *BufferedPipe) TryRead(b []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed && len(p.buffer) == 0 {
		return 0, io.EOF
	}

	if len(p.buffer) == 0 {
		return 0, ErrBufferEmpty
	}

	n = copy(b, p.buffer)
	p.buffer = p.buffer[n:]
	p.writeCond.Signal()
	return n, nil
}

// TryWrite 尝试非阻塞写入
func (p *BufferedPipe) TryWrite(b []byte) (n int, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return 0, ErrPipeClosed
	}

	available := p.bufferSize - len(p.buffer)
	if available == 0 {
		return 0, ErrBufferFull
	}

	toWrite := len(b)
	if toWrite > available {
		toWrite = available
	}
	if toWrite > p.maxSize {
		toWrite = p.maxSize
	}

	p.buffer = append(p.buffer, b[:toWrite]...)
	p.readCond.Signal()
	return toWrite, nil
}

// BufferLen 返回当前缓冲区中的数据长度（测试用）
func (p *BufferedPipe) BufferLen() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.buffer)
}

// AvailableSpace 返回可用空间大小（测试用）
func (p *BufferedPipe) AvailableSpace() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.bufferSize - len(p.buffer)
}
