// Package zop 实现 Zpt 混淆协议出站连接器
// 整合 Zop 混淆协议和 QUIC 传输层
package zop

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/SxAllyn/zpt/internal/protocol/zop"
	"github.com/SxAllyn/zpt/internal/transport"
)

// Config Zop出站配置
type Config struct {
	// 服务器地址（host:port）
	ServerAddr string
	// QUIC配置
	QUICConfig transport.QUICConfig
	// Zop混淆配置
	ZopConfig *zop.Config
	// 连接超时
	Timeout time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ServerAddr: "localhost:443",
		QUICConfig: transport.DefaultQUICConfig(),
		ZopConfig:  zop.DefaultConfig(),
		Timeout:    30 * time.Second,
	}
}

// Outbound Zop出站连接器
type Outbound struct {
	config *Config
	mu     sync.RWMutex
	// 活跃传输
	transport zop.Transport
	// 底层QUIC连接
	quicConn io.ReadWriteCloser
	// 是否已关闭
	closed bool
}

// New 创建新的Zop出站连接器
func New(config *Config) (*Outbound, error) {
	if config == nil {
		config = DefaultConfig()
	}

	return &Outbound{
		config: config,
		closed: false,
	}, nil
}

// Dial 建立到目标地址的连接
func (o *Outbound) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return o.DialContext(ctx, network, address)
}

// DialContext 使用上下文建立连接
func (o *Outbound) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil, errors.New("出站连接器已关闭")
	}

	// 设置超时上下文
	if o.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.config.Timeout)
		defer cancel()
	}

	// 建立QUIC连接（Mock实现）
	quicTransport := transport.NewQUICTransport(o.config.QUICConfig)
	if err := quicTransport.Dial(ctx, "tcp", o.config.ServerAddr); err != nil {
		return nil, fmt.Errorf("建立QUIC连接失败: %w", err)
	}
	quicConn := quicTransport // quicTransport 实现了 io.ReadWriteCloser

	// 创建Zop传输
	zopTransport, err := zop.NewTransport(o.config.ZopConfig, quicConn)
	if err != nil {
		quicConn.Close()
		return nil, fmt.Errorf("创建Zop传输失败: %w", err)
	}

	// 保存连接和传输
	o.quicConn = quicConn
	o.transport = zopTransport

	// 创建连接包装器
	conn := &ZopConn{
		transport: zopTransport,
		quicConn:  quicConn,
		localAddr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0},
		remoteAddr: &net.TCPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 443,
		},
	}

	return conn, nil
}

// Close 关闭出站连接器
func (o *Outbound) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return nil
	}
	o.closed = true

	var errs []error

	if o.transport != nil {
		if err := o.transport.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭Zop传输失败: %w", err))
		}
		o.transport = nil
	}

	// QUIC连接由transport管理，无需单独关闭
	if o.quicConn != nil {
		o.quicConn = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭出站连接器时发生错误: %v", errs)
	}
	return nil
}

// GetStats 获取统计信息
func (o *Outbound) GetStats() (stats zop.TransportStats, err error) {
	o.mu.RLock()
	defer o.mu.RUnlock()

	if o.transport == nil {
		return stats, errors.New("传输未初始化")
	}

	return o.transport.GetStats(), nil
}

// SwitchMode 切换到指定伪装形态
func (o *Outbound) SwitchMode(ctx context.Context, mode zop.Mode) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.closed {
		return errors.New("出站连接器已关闭")
	}

	if o.transport == nil {
		return errors.New("传输未初始化")
	}

	return o.transport.Switch(ctx, mode)
}

// ZopConn Zop连接包装器
type ZopConn struct {
	transport     zop.Transport
	quicConn      io.ReadWriteCloser
	localAddr     net.Addr
	remoteAddr    net.Addr
	readDeadline  time.Time
	writeDeadline time.Time
	mu            sync.RWMutex
}

// Read 读取数据
func (c *ZopConn) Read(b []byte) (n int, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 检查读取截止时间
	if !c.readDeadline.IsZero() && time.Now().After(c.readDeadline) {
		return 0, errors.New("读取超时")
	}

	if c.transport == nil {
		return 0, io.EOF
	}

	return c.transport.Read(b)
}

// Write 写入数据
func (c *ZopConn) Write(b []byte) (n int, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// 检查写入截止时间
	if !c.writeDeadline.IsZero() && time.Now().After(c.writeDeadline) {
		return 0, errors.New("写入超时")
	}

	if c.transport == nil {
		return 0, io.EOF
	}

	return c.transport.Write(b)
}

// Close 关闭连接
func (c *ZopConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	var errs []error

	if c.transport != nil {
		if err := c.transport.Close(); err != nil {
			errs = append(errs, fmt.Errorf("关闭Zop传输失败: %w", err))
		}
		c.transport = nil
	}

	// QUIC连接由transport管理，无需单独关闭
	if c.quicConn != nil {
		c.quicConn = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭连接时发生错误: %v", errs)
	}
	return nil
}

// LocalAddr 返回本地地址
func (c *ZopConn) LocalAddr() net.Addr {
	return c.localAddr
}

// RemoteAddr 返回远程地址
func (c *ZopConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// SetDeadline 设置截止时间
func (c *ZopConn) SetDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.readDeadline = t
	c.writeDeadline = t
	return nil
}

// SetReadDeadline 设置读取截止时间
func (c *ZopConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.readDeadline = t
	return nil
}

// SetWriteDeadline 设置写入截止时间
func (c *ZopConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writeDeadline = t
	return nil
}
