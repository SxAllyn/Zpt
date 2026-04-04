// Package tunproxy 提供TUN设备与代理出站的集成，实现透明代理功能
package tunproxy

import (
	"sync"
	"time"
)

// ConnectionTracker 连接跟踪器
type ConnectionTracker struct {
	maxConnections int
	connections    map[string]*TrackedConnection
	mu             sync.RWMutex
}

// TrackedConnection 被跟踪的连接
type TrackedConnection struct {
	ID          string
	SrcIP       string
	SrcPort     uint16
	DstIP       string
	DstPort     uint16
	CreatedAt   time.Time
	LastActive  time.Time
	BytesUp     uint64
	BytesDown   uint64
	PacketsUp   uint64
	PacketsDown uint64
	Closed      bool
}

// NewConnectionTracker 创建连接跟踪器
func NewConnectionTracker(maxConnections int) *ConnectionTracker {
	return &ConnectionTracker{
		maxConnections: maxConnections,
		connections:    make(map[string]*TrackedConnection),
	}
}

// AddConnection 添加连接跟踪
func (ct *ConnectionTracker) AddConnection(id, srcIP string, srcPort uint16, dstIP string, dstPort uint16) bool {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if len(ct.connections) >= ct.maxConnections {
		return false
	}

	ct.connections[id] = &TrackedConnection{
		ID:          id,
		SrcIP:       srcIP,
		SrcPort:     srcPort,
		DstIP:       dstIP,
		DstPort:     dstPort,
		CreatedAt:   time.Now(),
		LastActive:  time.Now(),
		BytesUp:     0,
		BytesDown:   0,
		PacketsUp:   0,
		PacketsDown: 0,
		Closed:      false,
	}

	return true
}

// RemoveConnection 移除连接跟踪
func (ct *ConnectionTracker) RemoveConnection(id string) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	delete(ct.connections, id)
}

// UpdateStats 更新连接统计
func (ct *ConnectionTracker) UpdateStats(id string, bytesUp, bytesDown uint64, packetsUp, packetsDown uint64) {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	if conn, exists := ct.connections[id]; exists {
		conn.LastActive = time.Now()
		conn.BytesUp += bytesUp
		conn.BytesDown += bytesDown
		conn.PacketsUp += packetsUp
		conn.PacketsDown += packetsDown
	}
}

// GetConnection 获取连接信息
func (ct *ConnectionTracker) GetConnection(id string) (*TrackedConnection, bool) {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	conn, exists := ct.connections[id]
	return conn, exists
}

// GetConnections 获取所有连接
func (ct *ConnectionTracker) GetConnections() []*TrackedConnection {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	conns := make([]*TrackedConnection, 0, len(ct.connections))
	for _, conn := range ct.connections {
		conns = append(conns, conn)
	}

	return conns
}

// Close 关闭所有连接
func (ct *ConnectionTracker) Close() {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	for id := range ct.connections {
		delete(ct.connections, id)
	}
}

// ActiveCount 获取活跃连接数
func (ct *ConnectionTracker) ActiveCount() int {
	ct.mu.RLock()
	defer ct.mu.RUnlock()

	return len(ct.connections)
}

// CleanupInactive 清理不活跃连接
func (ct *ConnectionTracker) CleanupInactive(timeout time.Duration) int {
	ct.mu.Lock()
	defer ct.mu.Unlock()

	now := time.Now()
	removed := 0

	for id, conn := range ct.connections {
		if now.Sub(conn.LastActive) > timeout {
			delete(ct.connections, id)
			removed++
		}
	}

	return removed
}
