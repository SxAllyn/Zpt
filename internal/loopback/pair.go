// Package loopback 提供环回测试组件
package loopback

import (
	"errors"
	"net"
	"sync"
)

// Pair 环回对，协调入站和出站连接
type Pair struct {
	connCh     chan net.Conn // 入站连接通道
	closeCh    chan struct{}
	closeOnce  sync.Once
	bufferSize int
}

// NewPair 创建新环回对
func NewPair(bufferSize int) *Pair {
	return &Pair{
		connCh:     make(chan net.Conn, bufferSize),
		closeCh:    make(chan struct{}),
		bufferSize: bufferSize,
	}
}

// Dial 拨号创建一个环回连接
// 返回客户端连接，并将服务器端连接放入队列供入站接受
func (p *Pair) Dial() (net.Conn, error) {
	select {
	case <-p.closeCh:
		return nil, errors.New("环回对已关闭")
	default:
	}

	// 创建管道对
	clientConn, serverConn := net.Pipe()

	// 将服务器端连接放入队列供入站接受
	select {
	case p.connCh <- serverConn:
		return clientConn, nil
	case <-p.closeCh:
		clientConn.Close()
		serverConn.Close()
		return nil, errors.New("环回对已关闭")
	}
}

// Accept 接受入站连接（从队列中获取服务器端连接）
func (p *Pair) Accept() (net.Conn, error) {
	select {
	case conn := <-p.connCh:
		return conn, nil
	case <-p.closeCh:
		return nil, errors.New("环回对已关闭")
	}
}

// Close 关闭环回对
func (p *Pair) Close() error {
	p.closeOnce.Do(func() {
		close(p.closeCh)
		// 清空连接通道
		for {
			select {
			case conn := <-p.connCh:
				conn.Close()
			default:
				return
			}
		}
	})
	return nil
}

// IsClosed 检查是否已关闭
func (p *Pair) IsClosed() bool {
	select {
	case <-p.closeCh:
		return true
	default:
		return false
	}
}
