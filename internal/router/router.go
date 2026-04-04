// Package router 提供数据包路由功能，支持TUN设备与出站连接之间的流量转发
package router

import (
	"net"
)

// Route 路由规则
type Route struct {
	// 目标网络（CIDR格式）
	Destination string
	// 网关地址（可选）
	Gateway string
	// 出站接口名称
	Interface string
	// 优先级
	Priority int
	// 是否启用
	Enabled bool
}

// PacketContext 数据包上下文
type PacketContext struct {
	// 原始数据包
	Packet []byte
	// 源地址
	Src net.IP
	// 目标地址
	Dst net.IP
	// 协议类型
	Protocol string
	// 接收时间戳
	Timestamp int64
	// 输入接口
	InputInterface string
}

// Router 路由引擎接口
type Router interface {
	// 启动路由引擎
	Start() error
	// 停止路由引擎
	Stop() error
	// 添加路由规则
	AddRoute(route *Route) error
	// 删除路由规则
	RemoveRoute(destination string) error
	// 获取路由表
	GetRoutes() []*Route
	// 路由数据包
	RoutePacket(ctx *PacketContext) error
	// 注册接口处理器
	RegisterInterface(name string, handler InterfaceHandler) error
	// 注销接口处理器
	UnregisterInterface(name string) error
	// 获取统计信息
	Stats() *RouterStats
}

// InterfaceHandler 接口处理器
type InterfaceHandler interface {
	// 发送数据包
	SendPacket(packet []byte) error
	// 获取接口信息
	GetInfo() *InterfaceInfo
}

// InterfaceInfo 接口信息
type InterfaceInfo struct {
	Name      string
	MTU       int
	Addresses []net.IPNet
	IsUp      bool
}

// RouterStats 路由引擎统计信息
type RouterStats struct {
	// 处理数据包数
	PacketsProcessed uint64
	// 路由数据包数
	PacketsRouted uint64
	// 丢弃数据包数
	PacketsDropped uint64
	// 错误数
	Errors uint64
	// 各接口统计
	InterfaceStats map[string]InterfaceStats
}

// InterfaceStats 接口统计信息
type InterfaceStats struct {
	PacketsSent     uint64
	PacketsReceived uint64
	BytesSent       uint64
	BytesReceived   uint64
	Errors          uint64
}
