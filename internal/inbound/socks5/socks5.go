// Package socks5 实现 SOCKS5 入站代理协议
package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
)

// Config SOCKS5服务器配置
type Config struct {
	// Addr 监听地址（例如 ":1080"）
	Addr string
	// AuthMethods 支持的认证方法
	AuthMethods []byte
	// Username 用户名（简单认证）
	Username string
	// Password 密码（简单认证）
	Password string
	// RequireAuth 是否需要认证
	RequireAuth bool
	// DialFunc 自定义拨号函数，如果为nil则使用net.Dial
	DialFunc func(ctx context.Context, network, address string) (net.Conn, error)
}

// DefaultConfig 返回默认配置（无认证，监听 :1080）
func DefaultConfig() *Config {
	return &Config{
		Addr:        ":1080",
		AuthMethods: []byte{0x00}, // 无认证
		RequireAuth: false,
		DialFunc:    nil,
	}
}

// Server SOCKS5服务器
type Server struct {
	config   *Config
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewServer 创建SOCKS5服务器
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
	listener, err := net.Listen("tcp", s.config.Addr)
	if err != nil {
		return fmt.Errorf("SOCKS5监听失败: %w", err)
	}
	s.listener = listener

	// 启动接受循环
	go s.acceptLoop()
	return nil
}

// acceptLoop 接受连接循环
func (s *Server) acceptLoop() {
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
			go s.handleConnection(conn)
		}
	}
}

// handleConnection 处理单个连接
func (s *Server) handleConnection(conn net.Conn) error {
	defer conn.Close()

	// 协商
	if err := s.negotiate(conn); err != nil {
		return err
	}

	// 处理请求
	return s.handleRequest(conn)
}

// negotiate 协商认证方法
func (s *Server) negotiate(conn net.Conn) error {
	// 读取客户端问候
	buf := make([]byte, 257) // 最大长度
	n, err := io.ReadAtLeast(conn, buf, 2)
	if err != nil {
		return err
	}

	ver := buf[0]
	if ver != 0x05 {
		return errors.New("不支持的SOCKS版本")
	}

	nmethods := int(buf[1])
	if n < 2+nmethods {
		// 读取剩余方法
		remaining := 2 + nmethods - n
		_, err = io.ReadFull(conn, buf[n:n+remaining])
		if err != nil {
			return err
		}
	}

	// 选择认证方法
	selectedMethod := byte(0xFF) // 无可用方法
	methods := buf[2 : 2+nmethods]

	// 检查是否支持无认证
	if !s.config.RequireAuth {
		for _, method := range methods {
			if method == 0x00 {
				selectedMethod = 0x00
				break
			}
		}
	}

	// 简单用户名/密码认证
	if s.config.RequireAuth && s.config.Username != "" && s.config.Password != "" {
		for _, method := range methods {
			if method == 0x02 {
				selectedMethod = 0x02
				break
			}
		}
	}

	// 回复选择的方法
	reply := []byte{0x05, selectedMethod}
	_, err = conn.Write(reply)
	if err != nil {
		return err
	}

	if selectedMethod == 0xFF {
		return errors.New("无支持的认证方法")
	}

	// 如果需要认证，进行认证
	if selectedMethod == 0x02 {
		return s.authenticate(conn)
	}

	return nil
}

// authenticate 进行用户名/密码认证
func (s *Server) authenticate(conn net.Conn) error {
	// 读取认证请求
	buf := make([]byte, 513) // 最大长度
	n, err := io.ReadAtLeast(conn, buf, 2)
	if err != nil {
		return err
	}

	ver := buf[0]
	if ver != 0x01 {
		return errors.New("不支持的认证版本")
	}

	ulen := int(buf[1])
	if n < 2+ulen {
		remaining := 2 + ulen - n
		_, err = io.ReadFull(conn, buf[n:n+remaining])
		if err != nil {
			return err
		}
	}

	username := string(buf[2 : 2+ulen])

	plen := int(buf[2+ulen])
	expectedLen := 2 + ulen + 1 + plen
	if n < expectedLen {
		remaining := expectedLen - n
		_, err = io.ReadFull(conn, buf[n:n+remaining])
		if err != nil {
			return err
		}
	}

	password := string(buf[2+ulen+1 : 2+ulen+1+plen])

	// 验证凭据
	if username != s.config.Username || password != s.config.Password {
		// 认证失败
		conn.Write([]byte{0x01, 0x01})
		return errors.New("认证失败")
	}

	// 认证成功
	conn.Write([]byte{0x01, 0x00})
	return nil
}

// handleRequest 处理SOCKS请求
func (s *Server) handleRequest(conn net.Conn) error {
	// 读取请求
	buf := make([]byte, 262) // 最大长度
	n, err := io.ReadAtLeast(conn, buf, 4)
	if err != nil {
		return err
	}

	ver := buf[0]
	if ver != 0x05 {
		return errors.New("不支持的SOCKS版本")
	}

	cmd := buf[1]
	if cmd != 0x01 { // 只支持CONNECT
		// 回复不支持的命令
		conn.Write([]byte{0x05, 0x07, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return errors.New("不支持的SOCKS命令")
	}

	addrType := buf[3]
	var host string
	var port uint16

	switch addrType {
	case 0x01: // IPv4
		if n < 10 {
			remaining := 10 - n
			_, err = io.ReadFull(conn, buf[n:n+remaining])
			if err != nil {
				return err
			}
			n = 10
		}
		host = net.IPv4(buf[4], buf[5], buf[6], buf[7]).String()
		port = binary.BigEndian.Uint16(buf[8:10])
		_ = 10
	case 0x03: // 域名
		domainLen := int(buf[4])
		if n < 5+domainLen+2 {
			remaining := 5 + domainLen + 2 - n
			_, err = io.ReadFull(conn, buf[n:n+remaining])
			if err != nil {
				return err
			}
			n = 5 + domainLen + 2
		}
		host = string(buf[5 : 5+domainLen])
		port = binary.BigEndian.Uint16(buf[5+domainLen : 5+domainLen+2])
		_ = 5 + domainLen + 2
	case 0x04: // IPv6
		if n < 22 {
			remaining := 22 - n
			_, err = io.ReadFull(conn, buf[n:n+remaining])
			if err != nil {
				return err
			}
			n = 22
		}
		host = net.IP(buf[4:20]).String()
		port = binary.BigEndian.Uint16(buf[20:22])
		_ = 22
	default:
		conn.Write([]byte{0x05, 0x08, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return errors.New("不支持的地址类型")
	}

	// 连接目标
	targetAddr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	var targetConn net.Conn
	if s.config.DialFunc != nil {
		targetConn, err = s.config.DialFunc(s.ctx, "tcp", targetAddr)
	} else {
		targetConn, err = net.Dial("tcp", targetAddr)
	}
	if err != nil {
		// 回复连接失败
		conn.Write([]byte{0x05, 0x04, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		return fmt.Errorf("连接目标失败: %w", err)
	}
	defer targetConn.Close()

	// 回复成功
	reply := make([]byte, 10)
	reply[0] = 0x05 // VER
	reply[1] = 0x00 // REP（成功）
	reply[2] = 0x00 // RSV
	reply[3] = 0x01 // ATYP（IPv4）
	// 绑定地址和端口（这里使用全零，表示服务器选择的地址）
	copy(reply[4:], []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	_, err = conn.Write(reply)
	if err != nil {
		return err
	}

	// 双向转发
	errCh := make(chan error, 2)

	// conn -> targetConn
	go func() {
		_, err := io.Copy(targetConn, conn)
		errCh <- err
	}()

	// targetConn -> conn
	go func() {
		_, err := io.Copy(conn, targetConn)
		errCh <- err
	}()

	// 等待任一方向错误或连接关闭
	<-errCh
	return nil
}

// Accept 实现 inbound.Listener 接口
func (s *Server) Accept() (net.Conn, error) {
	// SOCKS5服务器通常不直接暴露Accept方法
	// 这里返回错误，表示不支持
	return nil, errors.New("SOCKS5服务器不支持Accept方法")
}

// Close 关闭服务器
func (s *Server) Close() error {
	s.cancel()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

// Addr 返回监听地址
func (s *Server) Addr() net.Addr {
	if s.listener != nil {
		return s.listener.Addr()
	}
	return nil
}

// Handle 实现 inbound.Handler 接口
func (s *Server) Handle(ctx context.Context, conn net.Conn) error {
	// SOCKS5服务器已经处理整个连接生命周期
	// 这个方法用于适配inbound.Handler接口
	return s.handleConnection(conn)
}
