// Package transport 提供传输层实现
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"time"

	utls "github.com/refraction-networking/utls" // 用于指纹伪装
)

// TLSConfig TLS传输配置
type TLSConfig struct {
	// ServerName 服务器名称（SNI）
	ServerName string

	// InsecureSkipVerify 是否跳过证书验证
	InsecureSkipVerify bool

	// RootCAs 根证书池
	RootCAs *x509.CertPool

	// ClientCertificates 客户端证书
	ClientCertificates []tls.Certificate

	// NextProtos ALPN协议列表
	NextProtos []string

	// MinVersion 最低TLS版本
	MinVersion uint16

	// MaxVersion 最高TLS版本
	MaxVersion uint16

	// CipherSuites 密码套件列表
	CipherSuites []uint16

	// CurvePreferences 曲线偏好
	CurvePreferences []tls.CurveID

	// SessionTicketsDisabled 是否禁用会话票据
	SessionTicketsDisabled bool

	// ClientSessionCache 客户端会话缓存
	ClientSessionCache tls.ClientSessionCache

	// Timeout 连接超时
	Timeout time.Duration

	// FingerprintConfig 指纹伪装配置
	FingerprintConfig *FingerprintConfig
}

// FingerprintConfig 指纹伪装配置
type FingerprintConfig struct {
	// Enabled 是否启用指纹伪装
	Enabled bool

	// FingerprintType 指纹类型（如 "chrome", "firefox", "safari" 等）
	FingerprintType string

	// CustomHello 自定义ClientHello（高级用法）
	CustomHello []byte
}

// DefaultTLSConfig 返回默认TLS配置（TLS 1.3，现代安全设置）
func DefaultTLSConfig() TLSConfig {
	return TLSConfig{
		ServerName:             "",
		InsecureSkipVerify:     false,
		RootCAs:                nil,
		ClientCertificates:     nil,
		NextProtos:             []string{"h2", "http/1.1"},
		MinVersion:             tls.VersionTLS13,
		MaxVersion:             tls.VersionTLS13,
		CipherSuites:           nil, // TLS 1.3使用固定套件
		CurvePreferences:       []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384},
		SessionTicketsDisabled: false,
		ClientSessionCache:     nil,
		Timeout:                30 * time.Second,
		FingerprintConfig: &FingerprintConfig{
			Enabled:         false,
			FingerprintType: "chrome",
		},
	}
}

// TLSTransport TLS传输实现
type TLSTransport struct {
	config      TLSConfig
	baseConn    net.Conn // 底层连接（TCP或其他）
	tlsConn     net.Conn // TLS连接（标准tls.Conn或utls.UConn）
	dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewTLSTransport 创建TLS传输
func NewTLSTransport(config TLSConfig) *TLSTransport {
	return &TLSTransport{
		config:      config,
		dialContext: defaultDialContext,
	}
}

// NewTLSTransportWithDialer 使用自定义拨号器创建TLS传输
func NewTLSTransportWithDialer(config TLSConfig, dialContext func(ctx context.Context, network, addr string) (net.Conn, error)) *TLSTransport {
	return &TLSTransport{
		config:      config,
		dialContext: dialContext,
	}
}

// defaultDialContext 默认拨号函数（使用net.Dialer）
func defaultDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	dialer := &net.Dialer{
		Timeout: 30 * time.Second,
	}
	return dialer.DialContext(ctx, network, addr)
}

// Dial 建立TLS连接
func (t *TLSTransport) Dial(ctx context.Context, network, addr string) error {
	if t.dialContext == nil {
		t.dialContext = defaultDialContext
	}

	// 建立底层连接
	baseConn, err := t.dialContext(ctx, network, addr)
	if err != nil {
		return fmt.Errorf("建立底层连接失败: %w", err)
	}
	t.baseConn = baseConn

	// 创建TLS配置
	tlsConfig := &tls.Config{
		ServerName:             t.config.ServerName,
		InsecureSkipVerify:     t.config.InsecureSkipVerify,
		RootCAs:                t.config.RootCAs,
		Certificates:           t.config.ClientCertificates,
		NextProtos:             t.config.NextProtos,
		MinVersion:             t.config.MinVersion,
		MaxVersion:             t.config.MaxVersion,
		CipherSuites:           t.config.CipherSuites,
		CurvePreferences:       t.config.CurvePreferences,
		SessionTicketsDisabled: t.config.SessionTicketsDisabled,
		ClientSessionCache:     t.config.ClientSessionCache,
	}

	// 如果启用指纹伪装，使用utls（如果可用）
	if t.config.FingerprintConfig != nil && t.config.FingerprintConfig.Enabled {
		tlsConn, err := t.dialWithFingerprint(ctx, baseConn, tlsConfig)
		if err != nil {
			baseConn.Close()
			return err
		}
		t.tlsConn = tlsConn
		return nil
	}

	// 标准TLS握手
	tlsConn := tls.Client(baseConn, tlsConfig)

	// 设置超时上下文
	if t.config.Timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, t.config.Timeout)
		defer cancel()

		// 启动握手
		handshakeDone := make(chan error, 1)
		go func() {
			handshakeDone <- tlsConn.Handshake()
		}()

		select {
		case err := <-handshakeDone:
			if err != nil {
				baseConn.Close()
				return fmt.Errorf("TLS握手失败: %w", err)
			}
		case <-timeoutCtx.Done():
			baseConn.Close()
			return fmt.Errorf("TLS握手超时: %w", timeoutCtx.Err())
		}
	} else {
		// 无超时握手
		if err := tlsConn.Handshake(); err != nil {
			baseConn.Close()
			return fmt.Errorf("TLS握手失败: %w", err)
		}
	}

	t.tlsConn = tlsConn
	return nil
}

// convertTLSCertificates 将标准TLS证书转换为utls证书
func convertTLSCertificates(tlsCerts []tls.Certificate) []utls.Certificate {
	if tlsCerts == nil {
		return nil
	}

	utlsCerts := make([]utls.Certificate, len(tlsCerts))
	for i, cert := range tlsCerts {
		// 复制证书数据
		utlsCerts[i] = utls.Certificate{
			Certificate: cert.Certificate,
			PrivateKey:  cert.PrivateKey,
			// 其他字段可能不需要
		}
	}
	return utlsCerts
}

// convertTLSCurvePreferences 将标准TLS曲线偏好转换为utls曲线偏好
func convertTLSCurvePreferences(tlsCurves []tls.CurveID) []utls.CurveID {
	if tlsCurves == nil {
		return nil
	}

	utlsCurves := make([]utls.CurveID, len(tlsCurves))
	for i, curve := range tlsCurves {
		// CurveID是uint16的类型别名，可以直接转换
		utlsCurves[i] = utls.CurveID(curve)
	}
	return utlsCurves
}

// utlsClientSessionCache 适配器，将tls.ClientSessionCache转换为utls.ClientSessionCache
// 暂时禁用，因为utls.ClientSessionState与标准库tls.ClientSessionState结构不同
// TODO: 实现完整的会话缓存适配
/*
type utlsClientSessionCache struct {
	cache tls.ClientSessionCache
}

func (u *utlsClientSessionCache) Get(sessionKey string) (*utls.ClientSessionState, bool) {
	if u.cache == nil {
		return nil, false
	}

	// 尝试获取标准TLS会话状态
	state, ok := u.cache.Get(sessionKey)
	if !ok || state == nil {
		return nil, false
	}

	// 转换为utls.ClientSessionState
	// 注意：这里简化处理，实际可能需要深度复制
	utlsState := &utls.ClientSessionState{
		SessionTicket:      state.SessionTicket,
		Vers:               state.Vers,
		CipherSuite:        state.CipherSuite,
		MasterSecret:       state.MasterSecret,
		ServerCertificates: convertTLSCertificates(state.ServerCertificates),
		HandshakeHash:      state.HandshakeHash,
	}

	return utlsState, true
}

func (u *utlsClientSessionCache) Put(sessionKey string, cs *utls.ClientSessionState) {
	if u.cache == nil || cs == nil {
		return
	}

	// 将utls.ClientSessionState转换为tls.ClientSessionState
	state := &tls.ClientSessionState{
		SessionTicket:      cs.SessionTicket,
		Vers:               cs.Vers,
		CipherSuite:        cs.CipherSuite,
		MasterSecret:       cs.MasterSecret,
		ServerCertificates: make([]tls.Certificate, len(cs.ServerCertificates)),
		HandshakeHash:      cs.HandshakeHash,
	}

	// 转换证书
	for i, cert := range cs.ServerCertificates {
		state.ServerCertificates[i] = tls.Certificate{
			Certificate: cert.Certificate,
			PrivateKey:  cert.PrivateKey,
		}
	}

	u.cache.Put(sessionKey, state)
}
*/

// dialWithFingerprint 使用指纹伪装建立TLS连接
func (t *TLSTransport) dialWithFingerprint(ctx context.Context, baseConn net.Conn, baseConfig *tls.Config) (net.Conn, error) {
	fpConfig := t.config.FingerprintConfig
	if fpConfig == nil || !fpConfig.Enabled {
		// 指纹伪装未启用，使用标准TLS
		tlsConn := tls.Client(baseConn, baseConfig)
		if err := tlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("TLS握手失败: %w", err)
		}
		return tlsConn, nil
	}

	// 指纹伪装已启用，使用utls
	// 根据指纹类型选择ClientHelloID
	var helloID utls.ClientHelloID
	switch fpConfig.FingerprintType {
	case "chrome":
		helloID = utls.HelloChrome_Auto
	case "firefox":
		helloID = utls.HelloFirefox_Auto
	case "safari":
		helloID = utls.HelloSafari_Auto
	case "ios":
		helloID = utls.HelloIOS_Auto
	case "edge":
		helloID = utls.HelloEdge_Auto
	case "opera":
		helloID = utls.HelloChrome_Auto // Opera指纹暂用Chrome代替
	case "random":
		helloID = utls.HelloRandomized
	case "randomized":
		helloID = utls.HelloRandomized
	default:
		// 默认使用Chrome指纹
		helloID = utls.HelloChrome_Auto
	}

	// 创建utls配置（包含类型转换）
	// 注意：ClientSessionCache暂时设为nil，因为utls.ClientSessionState与标准库不兼容
	utlsConfig := &utls.Config{
		ServerName:             baseConfig.ServerName,
		InsecureSkipVerify:     baseConfig.InsecureSkipVerify,
		RootCAs:                baseConfig.RootCAs,
		Certificates:           convertTLSCertificates(baseConfig.Certificates),
		NextProtos:             baseConfig.NextProtos,
		MinVersion:             baseConfig.MinVersion,
		MaxVersion:             baseConfig.MaxVersion,
		CipherSuites:           baseConfig.CipherSuites,
		CurvePreferences:       convertTLSCurvePreferences(baseConfig.CurvePreferences),
		SessionTicketsDisabled: baseConfig.SessionTicketsDisabled,
		ClientSessionCache:     nil,
	}

	// 创建utls连接
	utlsConn := utls.UClient(baseConn, utlsConfig, helloID)

	// 设置超时上下文
	if t.config.Timeout > 0 {
		timeoutCtx, cancel := context.WithTimeout(ctx, t.config.Timeout)
		defer cancel()

		handshakeDone := make(chan error, 1)
		go func() {
			handshakeDone <- utlsConn.Handshake()
		}()

		select {
		case err := <-handshakeDone:
			if err != nil {
				return nil, fmt.Errorf("utls握手失败: %w", err)
			}
		case <-timeoutCtx.Done():
			return nil, fmt.Errorf("utls握手超时: %w", timeoutCtx.Err())
		}
	} else {
		// 无超时握手
		if err := utlsConn.Handshake(); err != nil {
			return nil, fmt.Errorf("utls握手失败: %w", err)
		}
	}

	return utlsConn, nil
}

// Read 读取数据
func (t *TLSTransport) Read(p []byte) (n int, err error) {
	if t.tlsConn == nil {
		return 0, fmt.Errorf("TLS连接未建立")
	}
	return t.tlsConn.Read(p)
}

// Write 写入数据
func (t *TLSTransport) Write(p []byte) (n int, err error) {
	if t.tlsConn == nil {
		return 0, fmt.Errorf("TLS连接未建立")
	}
	return t.tlsConn.Write(p)
}

// Close 关闭连接
func (t *TLSTransport) Close() error {
	var errs []error

	if t.tlsConn != nil {
		if err := t.tlsConn.Close(); err != nil {
			errs = append(errs, err)
		}
		t.tlsConn = nil
	}

	if t.baseConn != nil {
		if err := t.baseConn.Close(); err != nil {
			errs = append(errs, err)
		}
		t.baseConn = nil
	}

	if len(errs) > 0 {
		return fmt.Errorf("关闭连接时出错: %v", errs)
	}
	return nil
}

// LocalAddr 返回本地地址
func (t *TLSTransport) LocalAddr() net.Addr {
	if t.tlsConn != nil {
		return t.tlsConn.LocalAddr()
	}
	if t.baseConn != nil {
		return t.baseConn.LocalAddr()
	}
	return nil
}

// RemoteAddr 返回远程地址
func (t *TLSTransport) RemoteAddr() net.Addr {
	if t.tlsConn != nil {
		return t.tlsConn.RemoteAddr()
	}
	if t.baseConn != nil {
		return t.baseConn.RemoteAddr()
	}
	return nil
}

// SetDeadline 设置截止时间
func (t *TLSTransport) SetDeadline(tm time.Time) error {
	if t.tlsConn == nil {
		return fmt.Errorf("TLS连接未建立")
	}
	return t.tlsConn.SetDeadline(tm)
}

// SetReadDeadline 设置读截止时间
func (t *TLSTransport) SetReadDeadline(tm time.Time) error {
	if t.tlsConn == nil {
		return fmt.Errorf("TLS连接未建立")
	}
	return t.tlsConn.SetReadDeadline(tm)
}

// SetWriteDeadline 设置写截止时间
func (t *TLSTransport) SetWriteDeadline(tm time.Time) error {
	if t.tlsConn == nil {
		return fmt.Errorf("TLS连接未建立")
	}
	return t.tlsConn.SetWriteDeadline(tm)
}

// ConnectionState 返回TLS连接状态
func (t *TLSTransport) ConnectionState() tls.ConnectionState {
	if t.tlsConn == nil {
		return tls.ConnectionState{}
	}

	// 尝试类型断言到标准tls.Conn
	if tlsConn, ok := t.tlsConn.(*tls.Conn); ok {
		return tlsConn.ConnectionState()
	}

	// 尝试类型断言到utls.UConn
	if utlsConn, ok := t.tlsConn.(*utls.UConn); ok {
		// 获取utls连接状态
		utlsState := utlsConn.ConnectionState()

		// utls.ConnectionState与tls.ConnectionState字段兼容
		// PeerCertificates和VerifiedChains已经是[]*x509.Certificate类型
		// 直接复制公共字段
		return tls.ConnectionState{
			Version:                     utlsState.Version,
			HandshakeComplete:           utlsState.HandshakeComplete,
			DidResume:                   utlsState.DidResume,
			CipherSuite:                 utlsState.CipherSuite,
			NegotiatedProtocol:          utlsState.NegotiatedProtocol,
			NegotiatedProtocolIsMutual:  utlsState.NegotiatedProtocolIsMutual,
			ServerName:                  utlsState.ServerName,
			PeerCertificates:            utlsState.PeerCertificates,
			VerifiedChains:              utlsState.VerifiedChains,
			SignedCertificateTimestamps: utlsState.SignedCertificateTimestamps,
			OCSPResponse:                utlsState.OCSPResponse,
			TLSUnique:                   utlsState.TLSUnique,
		}
	}

	// 未知连接类型
	return tls.ConnectionState{}
}

// IsConnected 检查是否已连接
func (t *TLSTransport) IsConnected() bool {
	return t.tlsConn != nil
}

// Reconnect 重新连接
func (t *TLSTransport) Reconnect(ctx context.Context, network, addr string) error {
	if err := t.Close(); err != nil {
		return fmt.Errorf("关闭旧连接失败: %w", err)
	}
	return t.Dial(ctx, network, addr)
}
