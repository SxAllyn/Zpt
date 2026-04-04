// Package zop 实现 Zpt 混淆协议
// 性能基准测试：测量零拷贝架构的性能指标
package zop

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/SxAllyn/zpt/internal/transport"
)

// BenchmarkZeroCopyWrite 基准测试零拷贝写入性能
func BenchmarkZeroCopyWrite(b *testing.B) {
	// 测试不同数据大小
	sizes := []int{
		1024,        // 1KB
		32 * 1024,   // 32KB
		128 * 1024,  // 128KB
		512 * 1024,  // 512KB
		1024 * 1024, // 1MB
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			// 准备测试数据
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 256)
			}

			// 创建内存管道
			pipe1, pipe2 := transport.NewBufferedPipe(1024 * 1024) // 1MB缓冲区
			defer pipe1.Close()
			defer pipe2.Close()

			// 创建HTTP/3传输实例
			config := &Config{
				EnabledModes: []Mode{ModeHTTP3},
				DefaultMode:  ModeHTTP3,
			}
			transport, err := newHTTP3Transport(config, pipe1)
			if err != nil {
				b.Fatalf("创建HTTP/3传输失败: %v", err)
			}
			defer transport.Close()

			// 启动接收goroutine（消耗数据，防止阻塞）
			received := make(chan int64, 1)
			go func() {
				var total int64
				buf := make([]byte, 32*1024)
				for {
					n, err := pipe2.Read(buf)
					if n > 0 {
						total += int64(n)
					}
					if err != nil {
						break
					}
				}
				received <- total
			}()

			b.ResetTimer()
			b.SetBytes(int64(size))

			for i := 0; i < b.N; i++ {
				// 写入数据
				n, err := transport.Write(data)
				if err != nil {
					b.Fatalf("写入失败: %v", err)
				}
				if n != size {
					b.Fatalf("写入长度不匹配: 预期 %d, 实际 %d", size, n)
				}
			}

			// 关闭传输，确保所有数据被接收
			transport.Close()
			pipe1.Close()

			// 等待接收完成
			<-received
		})
	}
}

// BenchmarkZeroCopyRead 基准测试零拷贝读取性能
func BenchmarkZeroCopyRead(b *testing.B) {
	// 测试不同数据大小
	sizes := []int{
		1024,        // 1KB
		32 * 1024,   // 32KB
		128 * 1024,  // 128KB
		512 * 1024,  // 512KB
		1024 * 1024, // 1MB
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			// 准备测试数据
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 256)
			}

			// 创建内存管道
			pipe1, pipe2 := transport.NewBufferedPipe(1024 * 1024) // 1MB缓冲区
			defer pipe1.Close()
			defer pipe2.Close()

			// 创建HTTP/3传输实例（在另一端）
			config := &Config{
				EnabledModes: []Mode{ModeHTTP3},
				DefaultMode:  ModeHTTP3,
			}
			sender, err := newHTTP3Transport(config, pipe1)
			if err != nil {
				b.Fatalf("创建HTTP/3传输失败: %v", err)
			}
			defer sender.Close()

			// 创建接收传输实例
			receiver, err := newHTTP3Transport(config, pipe2)
			if err != nil {
				b.Fatalf("创建HTTP/3传输失败: %v", err)
			}
			defer receiver.Close()

			// 启动发送goroutine
			done := make(chan struct{})
			go func() {
				for i := 0; i < b.N; i++ {
					_, err := sender.Write(data)
					if err != nil {
						b.Errorf("发送失败: %v", err)
					}
				}
				close(done)
			}()

			b.ResetTimer()
			b.SetBytes(int64(size))

			// 接收数据
			for i := 0; i < b.N; i++ {
				received := make([]byte, size)
				total := 0
				for total < size {
					n, err := receiver.Read(received[total:])
					if err != nil && err != io.EOF {
						b.Fatalf("读取失败: %v", err)
					}
					if n == 0 && err == io.EOF {
						break
					}
					total += n
				}
				if total != size {
					b.Fatalf("读取长度不匹配: 预期 %d, 实际 %d", size, total)
				}
				// 验证数据（可选，会增加开销）
				// if !bytes.Equal(data, received) {
				// 	b.Fatal("数据不匹配")
				// }
			}

			// 等待发送完成
			<-done
		})
	}
}

// BenchmarkStreamObfuscate 基准测试流式混淆器性能
func BenchmarkStreamObfuscate(b *testing.B) {
	// 创建HTTP/3混淆器
	config := &Config{
		EnabledModes: []Mode{ModeHTTP3},
		DefaultMode:  ModeHTTP3,
	}
	obf, err := newHTTP3Obfuscator(config)
	if err != nil {
		b.Fatalf("创建HTTP/3混淆器失败: %v", err)
	}
	streamObf, ok := obf.(StreamObfuscator)
	if !ok {
		b.Fatal("混淆器不支持流式接口")
	}

	// 测试不同数据大小
	sizes := []int{
		32 * 1024,   // 32KB
		128 * 1024,  // 128KB
		512 * 1024,  // 512KB
		1024 * 1024, // 1MB
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%d", size), func(b *testing.B) {
			// 准备测试数据
			data := make([]byte, size)
			for i := range data {
				data[i] = byte(i % 256)
			}

			b.ResetTimer()
			b.SetBytes(int64(size))

			for i := 0; i < b.N; i++ {
				// 创建源和目标缓冲区
				src := bytes.NewReader(data)
				var dst bytes.Buffer

				// 执行流式混淆
				written, err := streamObf.StreamObfuscate(context.Background(), src, &dst)
				if err != nil {
					b.Fatalf("StreamObfuscate失败: %v", err)
				}
				if written != int64(size) {
					b.Fatalf("写入长度不匹配: 预期 %d, 实际 %d", size, written)
				}

				// 重置缓冲区
				dst.Reset()
			}
		})
	}
}

// BenchmarkZeroCopyAllocations 基准测试零拷贝内存分配
func BenchmarkZeroCopyAllocations(b *testing.B) {
	const size = 128 * 1024 // 128KB
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 创建内存管道
	pipe1, pipe2 := transport.NewBufferedPipe(1024 * 1024)
	defer pipe1.Close()
	defer pipe2.Close()

	// 创建HTTP/3传输实例
	config := &Config{
		EnabledModes: []Mode{ModeHTTP3},
		DefaultMode:  ModeHTTP3,
	}
	transport, err := newHTTP3Transport(config, pipe1)
	if err != nil {
		b.Fatalf("创建HTTP/3传输失败: %v", err)
	}
	defer transport.Close()

	// 启动接收goroutine
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 32*1024)
		for {
			_, err := pipe2.Read(buf)
			if err != nil {
				break
			}
		}
		close(done)
	}()

	b.ResetTimer()

	// 运行基准测试，记录分配次数
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		n, err := transport.Write(data)
		if err != nil {
			b.Fatalf("写入失败: %v", err)
		}
		if n != size {
			b.Fatalf("写入长度不匹配: 预期 %d, 实际 %d", size, n)
		}
	}

	// 关闭并等待
	transport.Close()
	<-done
}
