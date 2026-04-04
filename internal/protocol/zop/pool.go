// Package zop 实现 Zpt 混淆协议
// 缓冲区池优化：减少内存分配，提高大数据传输性能
package zop

import (
	"github.com/SxAllyn/zpt/internal/pool"
)

const (
	// SmallBufferSize 小缓冲区大小（4KB），用于控制帧和头部
	SmallBufferSize = 4 * 1024
	// LargeBufferSize 大缓冲区大小（64KB），用于数据载荷
	LargeBufferSize = 64 * 1024
)

var (
// 保留变量声明，实际使用新的pool包
// 为向后兼容保留空结构
)

// GetSmallBuffer 从小缓冲区池获取字节切片
// 使用新的四级内存池（4K请求返回8K池缓冲区）
func GetSmallBuffer() []byte {
	return pool.Alloc(SmallBufferSize)
}

// PutSmallBuffer 将字节切片归还小缓冲区池
func PutSmallBuffer(buf []byte) {
	pool.Free(buf)
}

// GetLargeBuffer 从大缓冲区池获取字节切片
// 使用新的四级内存池（64K请求返回128K池缓冲区）
func GetLargeBuffer() []byte {
	return pool.Alloc(LargeBufferSize)
}

// PutLargeBuffer 将字节切片归还大缓冲区池
func PutLargeBuffer(buf []byte) {
	pool.Free(buf)
}

// GetBuffer 根据大小智能选择缓冲区池
// 使用新的四级内存池实现零拷贝优化
func GetBuffer(size int) []byte {
	return pool.Alloc(size)
}

// PutBuffer 根据容量智能归还缓冲区
// 使用新的四级内存池实现零拷贝优化
func PutBuffer(buf []byte) {
	pool.Free(buf)
}
