// Package zop 实现 Zop 混淆协议入站处理器
// Zop (Zpt Obfuscation Protocol) 入站服务器，接受客户端混淆连接
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

// Config Zop入站服务器配置
type Config struct {
	// Addr 监听地址（例如 ":8443"）
	Addr string
	// QUIC配置
	QUICConfig transport.QUICConfig
	// Zop混淆配置
	ZopConfig *zop.Config
	// DialFunc 自定义拨号函数，用于转发解混淆后的流量
	// 如果为nil，则连接需要由外部处理器处理
	DialFunc func(ctx context.Context, network, address string) (net.Conn, error)
	// 连接超时
	Timeout time.Duration
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Addr:       ":8443",
		QUICConfig: transport.DefaultQUICConfig(),
		ZopConfig:  zop.DefaultConfig(),
		DialFunc:   nil,
		Timeout:    30 * time.Second,
	}
}

// Server Zop入站服务器
type Server struct {
	config   *Config
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// NewServer 创建Zop入站服务器
func NewServer(config *Config) *Server {
	if config == nil {
		config = DefaultConfig()
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		config: config,
		ctx:    ctx,
		cancel: cancel,
	}
}

// Listen 开始监听
func (s *Server) Listen(ctx context.Context) error {
	// 目前使用TCP监听模拟QUIC监听
	// TODO: 替换为真实QUIC监听器（quic-go）
	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("Zop监听失败: %w", err)
	}
	s.listener = listener

	// 启动接受循环
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// acceptLoop 接受连接循环
func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			conn, err := s.listener.Accept()
			if err != nil {
				// 监听器已关闭
				if errors.Is(err, net.ErrClosed) {
					return
				}
				// 记录错误，继续接受
				continue
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handleConnection(conn)
			}()
		}
	}
}

// handleConnection 处理单个连接
func (s *Server) handleConnection(conn net.Conn) error {
	defer conn.Close()

	// 创建QUIC传输包装器（Mock）
	quicTransport := &mockQUICConn{Conn: conn}

	// 创建Zop传输
	zopTransport, err := zop.NewTransport(s.config.ZopConfig, quicTransport)
	if err != nil {
		return fmt.Errorf("创建Zop传输失败: %w", err)
	}

	// 创建Zop连接包装器
	zopConn := &ZopConn{
		transport:  zopTransport,
		quicConn:   quicTransport,
		localAddr:  conn.LocalAddr(),
		remoteAddr: conn.RemoteAddr(),
	}

	// 如果有自定义拨号器，则转发流量
	if s.config.DialFunc != nil {
		return s.forwardConnection(zopConn)
	}

	// 否则，连接需要由外部处理器处理
	// 这里可以返回连接给上层，但为了简化，直接关闭
	// 实际实现中应该提供回调机制
	return errors.New("未提供拨号器，无法处理连接")
}

// forwardConnection 转发连接流量
func (s *Server) forwardConnection(zopConn *ZopConn) error {
	// 建立到目标地址的连接
	// 注意：这里的目标地址需要从协议中解析
	// 简化版本：假设拨号器知道目标地址
	ctx, cancel := context.WithTimeout(s.ctx, s.config.Timeout)
	defer cancel()

	// 这里应该从Zop协议中解析目标地址
	// 简化：使用默认地址
	targetConn, err := s.config.DialFunc(ctx, "tcp", "localhost:8080")
	if err != nil {
		return fmt.Errorf("建立目标连接失败: %w", err)
	}
	defer targetConn.Close()

	// 双向转发
	return s.bidirectionalCopy(zopConn, targetConn)
}

// bidirectionalCopy 双向复制数据
func (s *Server) bidirectionalCopy(src, dst net.Conn) error {
	var wg sync.WaitGroup
	wg.Add(2)

	// 从源复制到目标
	go func() {
		defer wg.Done()
		io.Copy(dst, src)
		dst.Close()
	}()

	// 从目标复制到源
	go func() {
		defer wg.Done()
		io.Copy(src, dst)
		src.Close()
	}()

	wg.Wait()
	return nil
}

// Close 关闭服务器
func (s *Server) Close() error {
	s.cancel()
	if s.listener != nil {
		s.listener.Close()
	}
	s.wg.Wait()
	return nil
}

// Addr 返回监听地址
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
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

// mockQUICConn 模拟QUIC连接
type mockQUICConn struct {
	net.Conn
}

// ReadWriteCloser 接口已通过嵌入net.Conn实现
