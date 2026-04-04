// Package pool 提供高性能零拷贝内存池
// 参考Xray-core实现，支持四级缓冲区：2K, 8K, 32K, 128K
package pool

import (
	"sync"
)

// 内存池配置常量
const (
	// 池数量和大小乘数
	numPools  = 4
	sizeMulti = 4

	// 各级缓冲区大小（字节）
	Size2K   = 2048   // 2KB - 小控制帧
	Size8K   = 8192   // 8KB - 中等数据
	Size32K  = 32768  // 32KB - 数据块
	Size128K = 131072 // 128KB - 大数据块
)

var (
	// 内存池数组
	pools [numPools]sync.Pool

	// 每个池对应的缓冲区大小
	poolSizes [numPools]int
)

// init 初始化内存池
func init() {
	size := Size2K
	for i := 0; i < numPools; i++ {
		poolSize := size
		pools[i] = sync.Pool{
			New: func() interface{} {
				return make([]byte, poolSize)
			},
		}
		poolSizes[i] = poolSize
		size *= sizeMulti
	}
}

// GetAllocator 返回适合指定大小的内存分配器
func GetAllocator(size int) func() []byte {
	for i, poolSize := range poolSizes {
		if size <= poolSize {
			pool := &pools[i]
			return func() []byte {
				return pool.Get().([]byte)
			}
		}
	}
	// 超过最大池大小，直接分配
	return func() []byte {
		return make([]byte, size)
	}
}

// GetReleaser 返回适合缓冲区容量的释放器
func GetReleaser(buf []byte) func([]byte) {
	capacity := cap(buf)
	for i, poolSize := range poolSizes {
		if capacity >= poolSize {
			pool := &pools[i]
			return func(b []byte) {
				// 清零前poolSize字节防止敏感数据残留
				for j := 0; j < poolSize && j < len(b); j++ {
					b[j] = 0
				}
				pool.Put(b[:poolSize])
			}
		}
	}
	// 缓冲区太小，由GC回收
	return func(b []byte) {
		// 小缓冲区不归还
	}
}

// Alloc 分配字节切片（零拷贝优化）
// 返回的切片长度至少为size，容量为池大小或分配大小
func Alloc(size int) []byte {
	for i, poolSize := range poolSizes {
		if size <= poolSize {
			buf := pools[i].Get().([]byte)
			if len(buf) > size {
				buf = buf[:size]
			}
			// 更新统计
			mu.Lock()
			stats.Allocations[i]++
			mu.Unlock()
			return buf
		}
	}
	// 超过最大池大小，直接分配
	// 注意：这里不计入池统计，但会通过heapAllocCount跟踪
	return make([]byte, size)
}

// Free 释放字节切片回池中
func Free(buf []byte) {
	capacity := cap(buf)
	for i, poolSize := range poolSizes {
		if capacity >= poolSize {
			// 清零前poolSize字节防止敏感数据残留
			for j := 0; j < poolSize && j < len(buf); j++ {
				buf[j] = 0
			}
			pools[i].Put(buf[:poolSize])
			// 更新统计
			mu.Lock()
			stats.Releases[i]++
			mu.Unlock()
			return
		}
	}
	// 小容量缓冲区不归还，由GC回收
}

// GetZeroCopyBuffer 获取零拷贝缓冲区（智能选择池）
// 如果size超过阈值，返回nil表示应使用零拷贝技术
func GetZeroCopyBuffer(size int) []byte {
	const zeroCopyThreshold = Size32K // 32KB以上建议零拷贝
	if size > zeroCopyThreshold {
		return nil // 调用者应使用零拷贝技术
	}
	return Alloc(size)
}

// PoolStats 内存池统计信息
type PoolStats struct {
	Allocations [numPools]int64
	Releases    [numPools]int64
}

var (
	stats PoolStats
	mu    sync.RWMutex
)

// GetStats 获取内存池统计信息
func GetStats() PoolStats {
	mu.RLock()
	defer mu.RUnlock()
	return stats
}

// ResetStats 重置统计信息
func ResetStats() {
	mu.Lock()
	defer mu.Unlock()
	stats = PoolStats{}
}

// GetPoolAllocCount 获取内存池总分配次数
func GetPoolAllocCount() uint32 {
	mu.RLock()
	defer mu.RUnlock()
	var total int64
	for _, count := range stats.Allocations {
		total += count
	}
	return uint32(total)
}
