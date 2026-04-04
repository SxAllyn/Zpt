// Package tun 提供 TUN 设备抽象层，支持跨平台虚拟网络接口
package tun

import (
	"net"
)

// Config TUN设备配置
type Config struct {
	// 设备名称（可选，为空时自动生成）
	Name string
	// MTU（最大传输单元），默认 1500
	MTU int
	// 分配的IP地址（CIDR格式，如 "10.0.0.1/24"）
	Address string
	// 网关地址（可选）
	Gateway string
	// DNS服务器地址（可选）
	DNS []string
	// 路由表（CIDR格式列表）
	Routes []string
	// 是否启用IPv6
	EnableIPv6 bool
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Name:       "",
		MTU:        1500,
		Address:    "10.0.0.1/24",
		Gateway:    "10.0.0.1",
		DNS:        []string{"8.8.8.8", "1.1.1.1"},
		Routes:     []string{"0.0.0.0/0"},
		EnableIPv6: false,
	}
}

// Packet 表示一个网络数据包
type Packet struct {
	// 原始数据
	Data []byte
	// 协议类型（IPv4/IPv6）
	Protocol string
	// 源地址
	Src net.IP
	// 目标地址
	Dst net.IP
	// 接收时间戳
	Timestamp int64
}

// Device TUN设备接口
type Device interface {
	// 启动设备
	Start() error
	// 停止设备
	Stop() error
	// 读取数据包（阻塞）
	ReadPacket() (*Packet, error)
	// 写入数据包
	WritePacket(packet *Packet) error
	// 获取设备配置
	Config() *Config
	// 获取设备文件描述符（用于select/poll）
	FD() (uintptr, error)
	// 获取设备统计信息
	Stats() *DeviceStats
	// 是否已启动
	IsRunning() bool
}

// DeviceStats 设备统计信息
type DeviceStats struct {
	// 接收数据包数
	PacketsReceived uint64
	// 发送数据包数
	PacketsSent uint64
	// 接收字节数
	BytesReceived uint64
	// 发送字节数
	BytesSent uint64
	// 错误数
	Errors uint64
}

// Handler 数据包处理器接口
type Handler interface {
	// 处理接收到的数据包
	HandlePacket(packet *Packet) error
}
