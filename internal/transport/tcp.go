// Package transport 提供传输层实现
package transport

import (
	"context"
	"fmt"
	"net"
	"time"
)

// TCPConfig TCP传输配置
type TCPConfig struct {
	// Address 目标地址（host:port）
	Address string

	// DialTimeout 连接超时
	DialTimeout time.Duration

	// KeepAlive 是否启用TCP KeepAlive
	KeepAlive bool

	// KeepAlivePeriod KeepAlive周期
	KeepAlivePeriod time.Duration

	// NoDelay 是否禁用Nagle算法
	NoDelay bool
}

// DefaultTCPConfig 返回默认TCP配置
func DefaultTCPConfig() TCPConfig {
	return TCPConfig{
		DialTimeout:     30 * time.Second,
		KeepAlive:       true,
		KeepAlivePeriod: 30 * time.Second,
		NoDelay:         true,
	}
}

// TCPTransport TCP传输实现
type TCPTransport struct {
	config TCPConfig
	conn   net.Conn
}

// NewTCPTransport 创建TCP传输
func NewTCPTransport(config TCPConfig) *TCPTransport {
	return &TCPTransport{
		config: config,
	}
}

// Dial 连接到目标地址
func (t *TCPTransport) Dial(ctx context.Context) error {
	dialer := &net.Dialer{
		Timeout: t.config.DialTimeout,
	}

	if t.config.KeepAlive {
		dialer.KeepAlive = t.config.KeepAlivePeriod
	}

	conn, err := dialer.DialContext(ctx, "tcp", t.config.Address)
	if err != nil {
		return fmt.Errorf("TCP连接失败: %w", err)
	}

	// 配置连接
	if tc, ok := conn.(*net.TCPConn); ok {
		if t.config.NoDelay {
			tc.SetNoDelay(true)
		}
		if t.config.KeepAlive {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(t.config.KeepAlivePeriod)
		}
	}

	t.conn = conn
	return nil
}

// Read 读取数据
func (t *TCPTransport) Read(p []byte) (n int, err error) {
	if t.conn == nil {
		return 0, fmt.Errorf("连接未建立")
	}
	return t.conn.Read(p)
}

// Write 写入数据
func (t *TCPTransport) Write(p []byte) (n int, err error) {
	if t.conn == nil {
		return 0, fmt.Errorf("连接未建立")
	}
	return t.conn.Write(p)
}

// Close 关闭连接
func (t *TCPTransport) Close() error {
	if t.conn == nil {
		return nil
	}
	err := t.conn.Close()
	t.conn = nil
	return err
}

// LocalAddr 返回本地地址
func (t *TCPTransport) LocalAddr() net.Addr {
	if t.conn == nil {
		return nil
	}
	return t.conn.LocalAddr()
}

// RemoteAddr 返回远程地址
func (t *TCPTransport) RemoteAddr() net.Addr {
	if t.conn == nil {
		return nil
	}
	return t.conn.RemoteAddr()
}

// SetDeadline 设置截止时间
func (t *TCPTransport) SetDeadline(tm time.Time) error {
	if t.conn == nil {
		return fmt.Errorf("连接未建立")
	}
	return t.conn.SetDeadline(tm)
}

// SetReadDeadline 设置读截止时间
func (t *TCPTransport) SetReadDeadline(tm time.Time) error {
	if t.conn == nil {
		return fmt.Errorf("连接未建立")
	}
	return t.conn.SetReadDeadline(tm)
}

// SetWriteDeadline 设置写截止时间
func (t *TCPTransport) SetWriteDeadline(tm time.Time) error {
	if t.conn == nil {
		return fmt.Errorf("连接未建立")
	}
	return t.conn.SetWriteDeadline(tm)
}

// IsConnected 检查是否已连接
func (t *TCPTransport) IsConnected() bool {
	return t.conn != nil
}

// Reconnect 重新连接
func (t *TCPTransport) Reconnect(ctx context.Context) error {
	if t.conn != nil {
		t.conn.Close()
		t.conn = nil
	}
	return t.Dial(ctx)
}
