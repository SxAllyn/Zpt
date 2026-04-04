// Package ztptls 实现 Ztp over TLS 出站连接器
// 整合 Zap 认证协议、Ztp 多路复用协议和 TLS 传输层
package ztptls

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/SxAllyn/zpt/internal/protocol/zap"
	"github.com/SxAllyn/zpt/internal/protocol/ztp"
	"github.com/SxAllyn/zpt/internal/transport"
)

// Config ZtpTLS出站配置
type Config struct {
	// 服务器地址（host:port）
	ServerAddr string
	// TLS配置
	TLSConfig transport.TLSConfig
	// Zap认证配置
	ZapConfig *zap.Config
	// Ztp会话配置
	ZtpConfig ztp.SessionConfig
	// 连接超时
	Timeout time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		ServerAddr: "localhost:443",
		TLSConfig:  transport.DefaultTLSConfig(),
		ZapConfig:  zap.DefaultConfig(),
		ZtpConfig:  ztp.DefaultSessionConfig(),
		Timeout:    30 * time.Second,
	}
}

// Outbound ZtpTLS出站连接器
type Outbound struct {
	config *Config
	mu     sync.RWMutex
	// 活跃会话
	session *ztp.Session
	// 底层TLS连接
	tlsConn *transport.TLSTransport
}

// NewOutbound 创建出站连接器
func NewOutbound(config *Config) *Outbound {
	if config == nil {
		config = DefaultConfig()
	}
	return &Outbound{
		config: config,
	}
}

// Dial 建立到目标地址的连接
func (o *Outbound) Dial(ctx context.Context, network, address string) (net.Conn, error) {
	return o.DialContext(ctx, network, address)
}

// DialContext 使用上下文建立连接
func (o *Outbound) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// 只支持TCP
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("只支持TCP网络")
	}

	// 确保有活跃会话
	session, err := o.ensureSession(ctx)
	if err != nil {
		return nil, fmt.Errorf("建立Ztp会话失败: %w", err)
	}

	// 在会话上打开新流
	stream, err := session.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("打开Ztp流失败: %w", err)
	}

	// 创建出站连接包装器
	conn := &ZtpTLSConn{
		stream:    stream,
		localAddr: nil, // 将在连接成功后设置
		remoteAddr: &net.TCPAddr{
			IP:   net.ParseIP("0.0.0.0"),
			Port: 0,
		},
		targetAddr: address,
	}

	return conn, nil
}

// ensureSession 确保有活跃的Ztp会话
func (o *Outbound) ensureSession(ctx context.Context) (*ztp.Session, error) {
	o.mu.RLock()
	session := o.session
	o.mu.RUnlock()

	if session != nil {
		// 检查会话是否仍然活跃
		// TODO: 添加会话健康检查
		return session, nil
	}

	// 需要创建新会话
	o.mu.Lock()
	defer o.mu.Unlock()

	// 双重检查
	if o.session != nil {
		return o.session, nil
	}

	// 创建新会话
	session, err := o.createSession(ctx)
	if err != nil {
		return nil, err
	}

	o.session = session
	return session, nil
}

// createSession 创建新的Ztp会话（包含TLS连接和Zap认证）
func (o *Outbound) createSession(ctx context.Context) (*ztp.Session, error) {
	// 设置超时上下文
	if o.config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.config.Timeout)
		defer cancel()
	}

	// 1. 建立TLS连接
	tlsTransport := transport.NewTLSTransport(o.config.TLSConfig)
	err := tlsTransport.Dial(ctx, "tcp", o.config.ServerAddr)
	if err != nil {
		return nil, fmt.Errorf("TLS连接失败: %w", err)
	}

	// 2. 执行Zap认证
	_, err = zap.Authenticate(ctx, tlsTransport, o.config.ZapConfig)
	if err != nil {
		tlsTransport.Close()
		return nil, fmt.Errorf("Zap认证失败: %w", err)
	}

	// 3. 创建Ztp会话（使用认证后的传输层）
	// 注意：这里直接使用TLS传输，实际实现中可能需要使用会话密钥进行加密
	session, err := ztp.NewSession(tlsTransport, o.config.ZtpConfig)
	if err != nil {
		tlsTransport.Close()
		return nil, fmt.Errorf("创建Ztp会话失败: %w", err)
	}

	// 4. 启动会话
	err = session.Start()
	if err != nil {
		tlsTransport.Close()
		return nil, fmt.Errorf("启动Ztp会话失败: %w", err)
	}

	// 保存TLS连接引用
	o.tlsConn = tlsTransport

	return session, nil
}

// Close 关闭出站连接器
func (o *Outbound) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	var errs []error

	if o.session != nil {
		if err := o.session.Close(); err != nil {
			errs = append(errs, err)
		}
		o.session = nil
	}

	if o.tlsConn != nil {
		if err := o.tlsConn.Close(); err != nil {
			errs = append(errs, err)
		}
		o.tlsConn = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭出站连接器时出错: %v", errs)
	}
	return nil
}

// ZtpTLSConn Ztp over TLS 连接实现
type ZtpTLSConn struct {
	stream     *ztp.Stream
	localAddr  net.Addr
	remoteAddr net.Addr
	targetAddr string
	closed     bool
	mu         sync.RWMutex
}

// Read 读取数据
func (c *ZtpTLSConn) Read(b []byte) (n int, err error) {
	if c.isClosed() {
		return 0, io.EOF
	}
	return c.stream.Read(b)
}

// Write 写入数据
func (c *ZtpTLSConn) Write(b []byte) (n int, err error) {
	if c.isClosed() {
		return 0, io.ErrClosedPipe
	}
	return c.stream.Write(b)
}

// Close 关闭连接
func (c *ZtpTLSConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return c.stream.Close()
}

// LocalAddr 返回本地地址
func (c *ZtpTLSConn) LocalAddr() net.Addr {
	return c.localAddr
}

// RemoteAddr 返回远程地址
func (c *ZtpTLSConn) RemoteAddr() net.Addr {
	return c.remoteAddr
}

// SetDeadline 设置截止时间
func (c *ZtpTLSConn) SetDeadline(t time.Time) error {
	// Ztp流不支持截止时间设置
	return nil
}

// SetReadDeadline 设置读截止时间
func (c *ZtpTLSConn) SetReadDeadline(t time.Time) error {
	// Ztp流不支持读截止时间设置
	return nil
}

// SetWriteDeadline 设置写截止时间
func (c *ZtpTLSConn) SetWriteDeadline(t time.Time) error {
	// Ztp流不支持写截止时间设置
	return nil
}

// isClosed 检查连接是否已关闭
func (c *ZtpTLSConn) isClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}
