// Package zop 实现 Zpt 混淆协议
// Zop (Zpt Obfuscation Protocol) 提供流量伪装功能，支持多种伪装形态
package zop

import (
	"context"
	"crypto/rand"
	"io"
	"time"
)

const (
	// ProtocolMagic 协议魔数
	ProtocolMagic uint32 = 0x5A4F5020 // "ZOP "
	// ProtocolVersion 协议版本
	ProtocolVersion uint8 = 0x01
)

// Mode 伪装形态枚举
type Mode uint8

const (
	// ModeHTTP3 HTTP/3 伪装
	ModeHTTP3 Mode = 0x01
	// ModeWebRTC WebRTC 伪装
	ModeWebRTC Mode = 0x02
	// ModeDoQ DNS over QUIC 伪装
	ModeDoQ Mode = 0x03
)

// SwitchPolicy 切换策略类型
type SwitchPolicy uint8

const (
	// SwitchPolicyTime 时间触发切换
	SwitchPolicyTime SwitchPolicy = 0x01
	// SwitchPolicyTraffic 流量量触发切换
	SwitchPolicyTraffic SwitchPolicy = 0x02
	// SwitchPolicyRandom 随机切换
	SwitchPolicyRandom SwitchPolicy = 0x03
	// SwitchPolicyAdaptive 自适应切换（基于网络状况）
	SwitchPolicyAdaptive SwitchPolicy = 0x04
)

// ObfuscationProfile 混淆配置
type ObfuscationProfile struct {
	// CurrentMode 当前伪装形态
	CurrentMode Mode
	// SwitchPolicy 切换策略
	SwitchPolicy SwitchPolicy
	// NextSwitchTime 下一次切换时间
	NextSwitchTime time.Time
	// 切换参数
	SwitchParams SwitchParams
}

// SwitchParams 切换参数
type SwitchParams struct {
	// 时间切换间隔（秒）
	TimeInterval time.Duration
	// 流量切换阈值（字节）
	TrafficThreshold uint64
	// 随机切换概率（0-1）
	RandomProbability float64
	// 自适应参数
	AdaptiveParams AdaptiveParams
}

// AdaptiveParams 自适应参数
type AdaptiveParams struct {
	// 延迟阈值（毫秒）
	LatencyThreshold time.Duration
	// 丢包率阈值（0-1）
	PacketLossThreshold float64
	// 最小稳定时间（秒）
	MinStableTime time.Duration
}

// Config Zop配置
type Config struct {
	// 启用的伪装形态列表
	EnabledModes []Mode
	// 默认伪装形态
	DefaultMode Mode
	// 是否启用动态切换
	EnableDynamicSwitch bool
	// 混淆配置
	Profiles []ObfuscationProfile
	// 形态特定配置
	ModeConfigs map[Mode]ModeConfig
}

// ModeConfig 形态特定配置
type ModeConfig struct {
	// HTTP/3 配置
	HTTP3 HTTP3Config
	// WebRTC 配置
	WebRTC WebRTCConfig
	// DoQ 配置
	DoQ DoQConfig
}

// HTTP3Config HTTP/3 伪装配置
type HTTP3Config struct {
	// 主机头
	HostHeader string
	// 路径模板
	PathTemplate string
	// 用户代理
	UserAgent string
	// 方法（GET/POST）
	Method string
	// 最大并发流数
	MaxConcurrentStreams uint32
}

// WebRTCConfig WebRTC 伪装配置
type WebRTCConfig struct {
	// ICE 服务器列表
	ICEServers []ICEServer
	// 数据通道标签
	DataChannelLabel string
	// SDP 模板
	SDPTemplate string
	// 是否启用双向数据
	Bidirectional bool
}

// ICEServer ICE 服务器配置
type ICEServer struct {
	URLs       []string
	Username   string
	Credential string
}

// DoQConfig DNS over QUIC 伪装配置
type DoQConfig struct {
	// DNS 服务器地址
	DNSServer string
	// 子域名模板
	SubdomainTemplate string
	// 查询类型（A/AAAA/TXT）
	QueryType string
	// 最大消息大小
	MaxMessageSize uint16
}

// Obfuscator 混淆器接口
type Obfuscator interface {
	// Obfuscate 混淆数据
	Obfuscate(ctx context.Context, data []byte) ([]byte, error)
	// Deobfuscate 解混淆数据
	Deobfuscate(ctx context.Context, data []byte) ([]byte, error)
	// GetMode 获取当前伪装形态
	GetMode() Mode
	// SwitchMode 切换伪装形态
	SwitchMode(newMode Mode) error
}

// Transport 伪装传输接口
type Transport interface {
	io.ReadWriteCloser
	// Mode 返回当前伪装形态
	Mode() Mode
	// Switch 切换到新形态
	Switch(ctx context.Context, newMode Mode) error
	// GetStats 获取统计信息
	GetStats() TransportStats
}

// TransportStats 传输统计
type TransportStats struct {
	// 发送字节数
	BytesSent uint64
	// 接收字节数
	BytesReceived uint64
	// 当前形态已使用时间
	CurrentModeDuration time.Duration
	// 切换次数
	SwitchCount uint32
	// 零拷贝读取次数
	ZeroCopyReadCount uint32
	// 零拷贝写入次数
	ZeroCopyWriteCount uint32
	// 内存池分配次数
	PoolAllocCount uint32
	// 堆分配次数（非池化）
	HeapAllocCount uint32
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		EnabledModes:        []Mode{ModeHTTP3, ModeWebRTC, ModeDoQ},
		DefaultMode:         ModeHTTP3,
		EnableDynamicSwitch: true,
		Profiles: []ObfuscationProfile{
			{
				CurrentMode:    ModeHTTP3,
				SwitchPolicy:   SwitchPolicyTime,
				NextSwitchTime: time.Now().Add(30 * time.Minute),
				SwitchParams: SwitchParams{
					TimeInterval:      30 * time.Minute,
					TrafficThreshold:  100 * 1024 * 1024, // 100MB
					RandomProbability: 0.1,
					AdaptiveParams: AdaptiveParams{
						LatencyThreshold:    100 * time.Millisecond,
						PacketLossThreshold: 0.05,
						MinStableTime:       5 * time.Minute,
					},
				},
			},
		},
		ModeConfigs: map[Mode]ModeConfig{
			ModeHTTP3: {
				HTTP3: HTTP3Config{
					HostHeader:           "example.com",
					PathTemplate:         "/api/v{version}/data/{id}",
					UserAgent:            "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36",
					Method:               "GET",
					MaxConcurrentStreams: 100,
				},
			},
			ModeWebRTC: {
				WebRTC: WebRTCConfig{
					ICEServers: []ICEServer{
						{
							URLs:       []string{"stun:stun.l.google.com:19302"},
							Username:   "",
							Credential: "",
						},
					},
					DataChannelLabel: "data",
					SDPTemplate:      "",
					Bidirectional:    true,
				},
			},
			ModeDoQ: {
				DoQ: DoQConfig{
					DNSServer:         "8.8.8.8:853",
					SubdomainTemplate: "{random}.d.{domain}.com",
					QueryType:         "TXT",
					MaxMessageSize:    1220, // DNS over QUIC 推荐值
				},
			},
		},
	}
}

// NewObfuscator 创建混淆器
func NewObfuscator(config *Config) (Obfuscator, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// 根据默认形态创建具体的混淆器实现
	return NewObfuscatorWithMode(config, config.DefaultMode)
}

// NewObfuscatorWithMode 创建指定形态的混淆器
func NewObfuscatorWithMode(config *Config, mode Mode) (Obfuscator, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// 检查形态是否启用
	enabled := false
	for _, m := range config.EnabledModes {
		if m == mode {
			enabled = true
			break
		}
	}
	if !enabled {
		mode = config.DefaultMode
	}

	// 根据形态创建具体的混淆器实现
	switch mode {
	case ModeHTTP3:
		return newHTTP3Obfuscator(config)
	case ModeWebRTC:
		return newWebRTCObfuscator(config)
	case ModeDoQ:
		return newDoQObfuscator(config)
	default:
		return newHTTP3Obfuscator(config)
	}
}

// NewTransport 创建伪装传输
func NewTransport(config *Config, conn io.ReadWriteCloser) (Transport, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// 创建基于默认形态的传输
	return NewTransportWithMode(config, config.DefaultMode, conn)
}

// NewTransportWithMode 创建指定形态的伪装传输
func NewTransportWithMode(config *Config, mode Mode, conn io.ReadWriteCloser) (Transport, error) {
	if config == nil {
		config = DefaultConfig()
	}

	// 检查形态是否启用
	enabled := false
	for _, m := range config.EnabledModes {
		if m == mode {
			enabled = true
			break
		}
	}
	if !enabled {
		mode = config.DefaultMode
	}

	// 根据形态创建具体的传输实现
	switch mode {
	case ModeHTTP3:
		return newHTTP3Transport(config, conn)
	case ModeWebRTC:
		return newWebRTCTransport(config, conn)
	case ModeDoQ:
		return newDoQTransport(config, conn)
	default:
		return newHTTP3Transport(config, conn)
	}
}

// baseObfuscator 基础混淆器实现
type baseObfuscator struct {
	config *Config
	mode   Mode
}

func (b *baseObfuscator) Obfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 基础实现：直接返回原数据（待具体形态实现）
	return data, nil
}

func (b *baseObfuscator) Deobfuscate(ctx context.Context, data []byte) ([]byte, error) {
	// 基础实现：直接返回原数据（待具体形态实现）
	return data, nil
}

func (b *baseObfuscator) GetMode() Mode {
	return b.mode
}

func (b *baseObfuscator) SwitchMode(newMode Mode) error {
	// 检查新形态是否启用
	for _, m := range b.config.EnabledModes {
		if m == newMode {
			b.mode = newMode
			return nil
		}
	}
	return nil
}

// StreamObfuscator 流式混淆器接口
type StreamObfuscator interface {
	// StreamObfuscate 从原始数据流读取，混淆后写入目标流
	// 返回写入的混淆后字节数和错误
	StreamObfuscate(ctx context.Context, src io.Reader, dst io.Writer) (int64, error)
	// StreamDeobfuscate 从混淆数据流读取，解混淆后写入目标流
	// 返回写入的解混淆后字节数和错误
	StreamDeobfuscate(ctx context.Context, src io.Reader, dst io.Writer) (int64, error)
	// GetMode 获取当前伪装形态
	GetMode() Mode
	// SwitchMode 切换伪装形态
	SwitchMode(newMode Mode) error
}

// generateRandomID 生成随机ID
func generateRandomID(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	rand.Read(b)
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}
