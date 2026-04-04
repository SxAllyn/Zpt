// 默认路由引擎实现
package router

import (
	"fmt"
	"net"
	"sync"
)

// DefaultRouter 默认路由引擎实现
type DefaultRouter struct {
	mu             sync.RWMutex
	routes         []*Route                    // 路由表
	interfaces     map[string]InterfaceHandler // 接口处理器映射
	interfaceMap   map[string]*InterfaceInfo   // 接口信息映射
	stats          *RouterStats
	interfaceStats map[string]*InterfaceStats // 各接口统计
	running        bool
}

// NewDefaultRouter 创建默认路由引擎
func NewDefaultRouter() *DefaultRouter {
	return &DefaultRouter{
		routes:         make([]*Route, 0),
		interfaces:     make(map[string]InterfaceHandler),
		interfaceMap:   make(map[string]*InterfaceInfo),
		stats:          &RouterStats{InterfaceStats: make(map[string]InterfaceStats)},
		interfaceStats: make(map[string]*InterfaceStats),
		running:        false,
	}
}

// Start 启动路由引擎
func (r *DefaultRouter) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("路由引擎已在运行")
	}

	r.running = true
	r.stats = &RouterStats{InterfaceStats: make(map[string]InterfaceStats)}
	r.interfaceStats = make(map[string]*InterfaceStats)

	// 初始化统计
	for name := range r.interfaces {
		r.interfaceStats[name] = &InterfaceStats{}
	}

	return nil
}

// Stop 停止路由引擎
func (r *DefaultRouter) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return fmt.Errorf("路由引擎未运行")
	}

	r.running = false
	return nil
}

// AddRoute 添加路由规则
func (r *DefaultRouter) AddRoute(route *Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if route == nil {
		return fmt.Errorf("路由规则不能为空")
	}

	// 验证目标网络格式
	_, _, err := net.ParseCIDR(route.Destination)
	if err != nil {
		return fmt.Errorf("无效的目标网络格式: %s", route.Destination)
	}

	// 检查是否已存在相同目标的路由
	for i, existing := range r.routes {
		if existing.Destination == route.Destination {
			// 替换现有路由
			r.routes[i] = route
			return nil
		}
	}

	// 添加新路由
	r.routes = append(r.routes, route)
	return nil
}

// RemoveRoute 删除路由规则
func (r *DefaultRouter) RemoveRoute(destination string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, route := range r.routes {
		if route.Destination == destination {
			// 删除路由
			r.routes = append(r.routes[:i], r.routes[i+1:]...)
			return nil
		}
	}

	return fmt.Errorf("未找到目标路由: %s", destination)
}

// GetRoutes 获取路由表
func (r *DefaultRouter) GetRoutes() []*Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 返回副本
	routes := make([]*Route, len(r.routes))
	copy(routes, r.routes)
	return routes
}

// RoutePacket 路由数据包
func (r *DefaultRouter) RoutePacket(ctx *PacketContext) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if !r.running {
		return fmt.Errorf("路由引擎未运行")
	}

	r.stats.PacketsProcessed++

	// 解析目标IP
	var dstIP net.IP
	if ctx.Protocol == "IPv4" || ctx.Protocol == "IPv6" {
		dstIP = ctx.Dst
	} else {
		// 尝试从数据包解析IP头（简化）
		if len(ctx.Packet) >= 20 && ctx.Packet[0]>>4 == 4 {
			// IPv4
			dstIP = net.IP(ctx.Packet[16:20])
		} else if len(ctx.Packet) >= 40 && ctx.Packet[0]>>4 == 6 {
			// IPv6
			dstIP = net.IP(ctx.Packet[24:40])
		} else {
			r.stats.PacketsDropped++
			return fmt.Errorf("无法解析数据包协议")
		}
	}

	// 查找匹配的路由
	var bestRoute *Route
	var bestMatchLen int

	for _, route := range r.routes {
		if !route.Enabled {
			continue
		}

		_, ipNet, err := net.ParseCIDR(route.Destination)
		if err != nil {
			continue
		}

		if ipNet.Contains(dstIP) {
			// 选择最具体的路由（前缀最长）
			ones, _ := ipNet.Mask.Size()
			if ones > bestMatchLen {
				bestMatchLen = ones
				bestRoute = route
			}
		}
	}

	if bestRoute == nil {
		r.stats.PacketsDropped++
		return fmt.Errorf("未找到匹配的路由规则")
	}

	// 查找接口处理器
	handler, ok := r.interfaces[bestRoute.Interface]
	if !ok {
		r.stats.PacketsDropped++
		return fmt.Errorf("接口未注册: %s", bestRoute.Interface)
	}

	// 发送数据包
	err := handler.SendPacket(ctx.Packet)
	if err != nil {
		r.stats.Errors++
		if stats, ok := r.interfaceStats[bestRoute.Interface]; ok {
			stats.Errors++
		}
		return fmt.Errorf("发送数据包失败: %w", err)
	}

	// 更新统计
	r.stats.PacketsRouted++
	if stats, ok := r.interfaceStats[bestRoute.Interface]; ok {
		stats.PacketsSent++
		stats.BytesSent += uint64(len(ctx.Packet))
	}

	return nil
}

// RegisterInterface 注册接口处理器
func (r *DefaultRouter) RegisterInterface(name string, handler InterfaceHandler) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if name == "" {
		return fmt.Errorf("接口名称不能为空")
	}

	if handler == nil {
		return fmt.Errorf("接口处理器不能为空")
	}

	if _, exists := r.interfaces[name]; exists {
		return fmt.Errorf("接口已注册: %s", name)
	}

	r.interfaces[name] = handler
	r.interfaceMap[name] = handler.GetInfo()
	r.interfaceStats[name] = &InterfaceStats{}

	return nil
}

// UnregisterInterface 注销接口处理器
func (r *DefaultRouter) UnregisterInterface(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.interfaces[name]; !exists {
		return fmt.Errorf("接口未注册: %s", name)
	}

	delete(r.interfaces, name)
	delete(r.interfaceMap, name)
	delete(r.interfaceStats, name)

	return nil
}

// Stats 获取统计信息
func (r *DefaultRouter) Stats() *RouterStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// 返回副本
	stats := *r.stats
	stats.InterfaceStats = make(map[string]InterfaceStats)

	for name, ifStats := range r.interfaceStats {
		stats.InterfaceStats[name] = *ifStats
	}

	return &stats
}
