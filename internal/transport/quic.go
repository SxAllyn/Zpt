// Package transport 提供传输层实现
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// QUICConfig QUIC传输配置
type QUICConfig struct {
	// Address 目标地址（host:port）
	Address string

	// ServerName 服务器名称（SNI）
	ServerName string

	// InsecureSkipVerify 是否跳过证书验证
	InsecureSkipVerify bool

	// NextProtos ALPN协议列表
	NextProtos []string

	// KeepAlivePeriod KeepAlive周期
	KeepAlivePeriod time.Duration

	// MaxIdleTimeout 最大空闲超时
	MaxIdleTimeout time.Duration

	// MaxIncomingStreams 最大入站流数
	MaxIncomingStreams int64

	// DisablePathMTUDiscovery 是否禁用路径MTU发现
	DisablePathMTUDiscovery bool

	// HandshakeTimeout 握手超时
	HandshakeTimeout time.Duration

	// Timeout 连接超时
	Timeout time.Duration
}

// DefaultQUICConfig 返回默认QUIC配置
func DefaultQUICConfig() QUICConfig {
	return QUICConfig{
		Address:                 "",
		ServerName:              "",
		InsecureSkipVerify:      false,
		NextProtos:              []string{"zpt-quic"},
		KeepAlivePeriod:         30 * time.Second,
		MaxIdleTimeout:          60 * time.Second,
		MaxIncomingStreams:      1024,
		DisablePathMTUDiscovery: false,
		HandshakeTimeout:        10 * time.Second,
		Timeout:                 30 * time.Second,
	}
}

// QUICTransport QUIC传输实现
type QUICTransport struct {
	config     QUICConfig
	connection *quic.Conn   // QUIC连接（真实模式）
	stream     *quic.Stream // QUIC流（真实模式）
	conn       net.Conn     // TCP连接（模拟模式）
	dialFunc   func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewQUICTransport 创建QUIC传输（Mock版本）
func NewQUICTransport(config QUICConfig) *QUICTransport {
	return &QUICTransport{
		config:   config,
		dialFunc: defaultDialContext,
	}
}

// NewQUICTransportWithDialer 使用自定义拨号器创建QUIC传输
func NewQUICTransportWithDialer(config QUICConfig, dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)) *QUICTransport {
	return &QUICTransport{
		config:   config,
		dialFunc: dialFunc,
	}
}

// Dial 建立QUIC连接
func (q *QUICTransport) Dial(ctx context.Context, network, addr string) error {
	// 模拟模式：如果提供了自定义拨号函数，使用TCP模拟QUIC
	if q.dialFunc != nil {
		// Mock实现：使用TCP连接模拟QUIC（向后兼容测试）
		conn, err := q.dialFunc(ctx, "tcp", addr)
		if err != nil {
			return fmt.Errorf("QUIC连接失败（Mock）: %w", err)
		}
		q.conn = conn
		return nil
	}

	// 真实QUIC模式
	// 使用地址（优先使用参数addr，其次使用配置中的Address）
	targetAddr := addr
	if targetAddr == "" && q.config.Address != "" {
		targetAddr = q.config.Address
	}
	if targetAddr == "" {
		return fmt.Errorf("QUIC连接地址未指定")
	}

	// 创建TLS配置
	tlsConfig := &tls.Config{
		ServerName:         q.config.ServerName,
		InsecureSkipVerify: q.config.InsecureSkipVerify,
		NextProtos:         q.config.NextProtos,
	}

	// 创建QUIC配置
	quicConfig := &quic.Config{
		KeepAlivePeriod:         q.config.KeepAlivePeriod,
		MaxIdleTimeout:          q.config.MaxIdleTimeout,
		MaxIncomingStreams:      q.config.MaxIncomingStreams,
		DisablePathMTUDiscovery: q.config.DisablePathMTUDiscovery,
		HandshakeIdleTimeout:    q.config.HandshakeTimeout,
	}

	// 建立QUIC连接
	connection, err := quic.DialAddr(ctx, targetAddr, tlsConfig, quicConfig)
	if err != nil {
		return fmt.Errorf("QUIC连接失败: %w", err)
	}
	q.connection = connection

	// 打开QUIC流
	stream, err := connection.OpenStream()
	if err != nil {
		connection.CloseWithError(0, "failed to open stream")
		return fmt.Errorf("打开QUIC流失败: %w", err)
	}
	q.stream = stream

	return nil
}

// Read 读取数据
func (q *QUICTransport) Read(p []byte) (n int, err error) {
	if q.stream != nil {
		return (*q.stream).Read(p)
	}
	if q.conn != nil {
		return q.conn.Read(p)
	}
	return 0, fmt.Errorf("QUIC连接未建立")
}

// Write 写入数据
func (q *QUICTransport) Write(p []byte) (n int, err error) {
	if q.stream != nil {
		return (*q.stream).Write(p)
	}
	if q.conn != nil {
		return q.conn.Write(p)
	}
	return 0, fmt.Errorf("QUIC连接未建立")
}

// Close 关闭连接
func (q *QUICTransport) Close() error {
	var errs []error

	// 关闭流（真实模式）
	if q.stream != nil {
		if err := (*q.stream).Close(); err != nil {
			errs = append(errs, err)
		}
		q.stream = nil
	}

	// 关闭QUIC连接（真实模式）
	if q.connection != nil {
		if err := (*q.connection).CloseWithError(0, "client closed"); err != nil {
			errs = append(errs, err)
		}
		q.connection = nil
	}

	// 关闭TCP连接（模拟模式）
	if q.conn != nil {
		if err := q.conn.Close(); err != nil {
			errs = append(errs, err)
		}
		q.conn = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭QUIC连接时出错: %v", errs)
	}
	return nil
}

// LocalAddr 返回本地地址
func (q *QUICTransport) LocalAddr() net.Addr {
	if q.connection != nil {
		return (*q.connection).LocalAddr()
	}
	if q.conn != nil {
		return q.conn.LocalAddr()
	}
	return nil
}

// RemoteAddr 返回远程地址
func (q *QUICTransport) RemoteAddr() net.Addr {
	if q.connection != nil {
		return (*q.connection).RemoteAddr()
	}
	if q.conn != nil {
		return q.conn.RemoteAddr()
	}
	return nil
}

// SetDeadline 设置截止时间
func (q *QUICTransport) SetDeadline(t time.Time) error {
	if q.stream != nil {
		return (*q.stream).SetDeadline(t)
	}
	if q.conn != nil {
		return q.conn.SetDeadline(t)
	}
	return fmt.Errorf("QUIC连接未建立")
}

// SetReadDeadline 设置读截止时间
func (q *QUICTransport) SetReadDeadline(t time.Time) error {
	if q.stream != nil {
		return (*q.stream).SetReadDeadline(t)
	}
	if q.conn != nil {
		return q.conn.SetReadDeadline(t)
	}
	return fmt.Errorf("QUIC连接未建立")
}

// SetWriteDeadline 设置写截止时间
func (q *QUICTransport) SetWriteDeadline(t time.Time) error {
	if q.stream != nil {
		return (*q.stream).SetWriteDeadline(t)
	}
	if q.conn != nil {
		return q.conn.SetWriteDeadline(t)
	}
	return fmt.Errorf("QUIC连接未建立")
}

// IsConnected 检查是否已连接
func (q *QUICTransport) IsConnected() bool {
	return q.stream != nil || q.conn != nil
}

// Reconnect 重新连接
func (q *QUICTransport) Reconnect(ctx context.Context, network, addr string) error {
	if err := q.Close(); err != nil {
		return fmt.Errorf("关闭旧连接失败: %w", err)
	}
	return q.Dial(ctx, network, addr)
}

// QUICStream QUIC流接口（占位符，用于未来扩展）
type QUICStream interface {
	net.Conn
	StreamID() uint64
}

// QUICConnection QUIC连接接口（占位符，用于未来扩展）
type QUICConnection interface {
	OpenStream() (QUICStream, error)
	OpenStreamSync(context.Context) (QUICStream, error)
	AcceptStream(context.Context) (QUICStream, error)
	CloseWithError(uint64, string) error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
}
