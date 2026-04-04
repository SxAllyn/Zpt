// Package config 提供Zpt代理的配置解析功能
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config 全局配置
type Config struct {
	// 日志配置
	Log *LogConfig `yaml:"log"`
	// SOCKS5入站配置
	Socks5 *Socks5Config `yaml:"socks5"`
	// TUN设备配置
	TUN *TUNConfig `yaml:"tun"`
	// 出站代理配置
	Outbound *OutboundConfig `yaml:"outbound"`
	// 路由规则
	Routes []*RouteConfig `yaml:"routes"`
	// 高级选项
	Advanced *AdvancedConfig `yaml:"advanced"`
}

// LogConfig 日志配置
type LogConfig struct {
	// 日志级别：debug, info, warn, error
	Level string `yaml:"level"`
	// 日志文件路径（空表示输出到标准输出）
	File string `yaml:"file"`
	// 是否启用彩色输出
	Color bool `yaml:"color"`
	// 是否启用时间戳
	Timestamp bool `yaml:"timestamp"`
}

// Socks5Config SOCKS5入站配置
type Socks5Config struct {
	// 是否启用
	Enabled bool `yaml:"enabled"`
	// 监听地址（如:1080）
	Listen string `yaml:"listen"`
	// 认证配置
	Auth *AuthConfig `yaml:"auth"`
	// UDP支持
	UDP bool `yaml:"udp"`
	// 连接超时（秒）
	Timeout int `yaml:"timeout"`
}

// AuthConfig 认证配置
type AuthConfig struct {
	// 是否启用认证
	Enabled bool `yaml:"enabled"`
	// 用户名
	Username string `yaml:"username"`
	// 密码
	Password string `yaml:"password"`
}

// TUNConfig TUN设备配置
type TUNConfig struct {
	// 是否启用
	Enabled bool `yaml:"enabled"`
	// 设备名称（可选）
	Name string `yaml:"name"`
	// MTU（最大传输单元）
	MTU int `yaml:"mtu"`
	// 分配的IP地址（CIDR格式，如 "10.0.0.1/24"）
	Address string `yaml:"address"`
	// 网关地址
	Gateway string `yaml:"gateway"`
	// DNS服务器列表
	DNS []string `yaml:"dns"`
	// 路由表（CIDR格式列表）
	Routes []string `yaml:"routes"`
	// 是否启用IPv6
	EnableIPv6 bool `yaml:"enable_ipv6"`
}

// OutboundConfig 出站代理配置
type OutboundConfig struct {
	// 代理类型：zop, direct, http, socks5
	Type string `yaml:"type"`
	// Zop代理配置
	Zop *ZopConfig `yaml:"zop"`
	// HTTP代理配置
	HTTP *HTTPConfig `yaml:"http"`
	// SOCKS5代理配置
	SOCKS5 *OutboundSOCKS5Config `yaml:"socks5"`
	// 直连配置
	Direct *DirectConfig `yaml:"direct"`
	// 代理选择策略：first, roundrobin, random
	Strategy string `yaml:"strategy"`
	// 代理组（多个出站代理）
	Groups []*OutboundGroup `yaml:"groups"`
}

// ZopConfig Zop代理配置
type ZopConfig struct {
	// 服务器地址
	Server string `yaml:"server"`
	// 服务器端口
	Port int `yaml:"port"`
	// 伪装类型：http3, webrtc, doq
	Disguise string `yaml:"disguise"`
	// 加密密钥
	Key string `yaml:"key"`
	// 压缩
	Compression bool `yaml:"compression"`
	// 连接超时（秒）
	Timeout int `yaml:"timeout"`
	// TLS配置
	TLS *TLSConfig `yaml:"tls"`
}

// TLSConfig TLS配置
type TLSConfig struct {
	// 是否启用TLS
	Enabled bool `yaml:"enabled"`
	// 服务器名称指示（SNI）
	ServerName string `yaml:"server_name"`
	// 跳过证书验证
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
	// 客户端证书文件
	CertFile string `yaml:"cert_file"`
	// 客户端密钥文件
	KeyFile string `yaml:"key_file"`
}

// HTTPConfig HTTP代理配置
type HTTPConfig struct {
	// 服务器地址
	Server string `yaml:"server"`
	// 服务器端口
	Port int `yaml:"port"`
	// 用户名
	Username string `yaml:"username"`
	// 密码
	Password string `yaml:"password"`
}

// OutboundSOCKS5Config 出站SOCKS5配置
type OutboundSOCKS5Config struct {
	// 服务器地址
	Server string `yaml:"server"`
	// 服务器端口
	Port int `yaml:"port"`
	// 用户名
	Username string `yaml:"username"`
	// 密码
	Password string `yaml:"password"`
}

// DirectConfig 直连配置
type DirectConfig struct {
	// 直连策略
	Strategy string `yaml:"strategy"`
}

// OutboundGroup 出站代理组
type OutboundGroup struct {
	// 组名称
	Name string `yaml:"name"`
	// 代理类型列表
	Types []string `yaml:"types"`
	// 延迟测试URL
	TestURL string `yaml:"test_url"`
	// 测试间隔（秒）
	TestInterval int `yaml:"test_interval"`
}

// RouteConfig 路由规则配置
type RouteConfig struct {
	// 规则名称
	Name string `yaml:"name"`
	// 目标网络（CIDR格式）
	Destination string `yaml:"destination"`
	// 出站代理名称或类型
	Outbound string `yaml:"outbound"`
	// 优先级
	Priority int `yaml:"priority"`
	// 是否启用
	Enabled bool `yaml:"enabled"`
}

// AdvancedConfig 高级配置
type AdvancedConfig struct {
	// 最大并发连接数
	MaxConnections int `yaml:"max_connections"`
	// 连接超时（秒）
	ConnectTimeout int `yaml:"connect_timeout"`
	// 读写缓冲区大小（字节）
	BufferSize int `yaml:"buffer_size"`
	// 是否启用TCP保持连接
	TCPKeepAlive bool `yaml:"tcp_keep_alive"`
	// TCP保持连接间隔（秒）
	TCPKeepAliveInterval int `yaml:"tcp_keep_alive_interval"`
	// 是否启用DNS缓存
	DNSCache bool `yaml:"dns_cache"`
	// DNS缓存大小
	DNSCacheSize int `yaml:"dns_cache_size"`
	// DNS缓存过期时间（秒）
	DNSCacheExpire int `yaml:"dns_cache_expire"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Log: &LogConfig{
			Level:     "info",
			File:      "",
			Color:     true,
			Timestamp: true,
		},
		Socks5: &Socks5Config{
			Enabled: true,
			Listen:  ":1080",
			Auth: &AuthConfig{
				Enabled:  false,
				Username: "",
				Password: "",
			},
			UDP:     false,
			Timeout: 30,
		},
		TUN: &TUNConfig{
			Enabled:    false,
			Name:       "",
			MTU:        1500,
			Address:    "10.0.0.1/24",
			Gateway:    "10.0.0.1",
			DNS:        []string{"8.8.8.8", "1.1.1.1"},
			Routes:     []string{"0.0.0.0/0"},
			EnableIPv6: false,
		},
		Outbound: &OutboundConfig{
			Type: "zop",
			Zop: &ZopConfig{
				Server:      "127.0.0.1",
				Port:        443,
				Disguise:    "http3",
				Key:         "",
				Compression: true,
				Timeout:     30,
				TLS: &TLSConfig{
					Enabled:            true,
					ServerName:         "",
					InsecureSkipVerify: false,
					CertFile:           "",
					KeyFile:            "",
				},
			},
			Strategy: "first",
			Groups:   []*OutboundGroup{},
		},
		Routes: []*RouteConfig{
			{
				Name:        "default",
				Destination: "0.0.0.0/0",
				Outbound:    "zop",
				Priority:    100,
				Enabled:     true,
			},
		},
		Advanced: &AdvancedConfig{
			MaxConnections:       1024,
			ConnectTimeout:       30,
			BufferSize:           16384,
			TCPKeepAlive:         true,
			TCPKeepAliveInterval: 30,
			DNSCache:             true,
			DNSCacheSize:         1024,
			DNSCacheExpire:       300,
		},
	}
}

// LoadFromFile 从文件加载配置
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	return LoadFromYAML(data)
}

// LoadFromYAML 从YAML数据加载配置
func LoadFromYAML(data []byte) (*Config, error) {
	config := DefaultConfig()

	if err := yaml.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("解析YAML配置失败: %w", err)
	}

	return config, nil
}

// SaveToFile 保存配置到文件
func (c *Config) SaveToFile(path string) error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Errorf("生成YAML配置失败: %w", err)
	}

	// 确保目录存在
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("写入配置文件失败: %w", err)
	}

	return nil
}

// Validate 验证配置有效性
func (c *Config) Validate() error {
	if c.Socks5 == nil {
		return fmt.Errorf("SOCKS5配置不能为空")
	}

	if c.TUN == nil {
		return fmt.Errorf("TUN配置不能为空")
	}

	if c.Outbound == nil {
		return fmt.Errorf("出站配置不能为空")
	}

	// 验证出站类型
	switch c.Outbound.Type {
	case "zop", "direct", "http", "socks5":
		// 有效类型
	default:
		return fmt.Errorf("不支持的出站类型: %s", c.Outbound.Type)
	}

	// 如果启用SOCKS5，验证监听地址
	if c.Socks5.Enabled && c.Socks5.Listen == "" {
		return fmt.Errorf("SOCKS5监听地址不能为空")
	}

	// 如果启用TUN，验证地址
	if c.TUN.Enabled && c.TUN.Address == "" {
		return fmt.Errorf("TUN设备地址不能为空")
	}

	return nil
}
