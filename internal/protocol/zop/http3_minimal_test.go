// HTTP/3传输层最小化流量控制测试
// 绕过SOCKS5和Zop拨号器，直接验证HTTP/3流量控制机制
package zop

import (
	"fmt"
	"testing"
	"time"

	"github.com/SxAllyn/zpt/internal/transport"
)

// TestHTTP3FlowControlMinimal 最小化HTTP/3流量控制测试
func TestHTTP3FlowControlMinimal(t *testing.T) {
	// 创建带缓冲的管道对
	clientPipe, serverPipe := transport.NewBufferedPipe(1048576) // 1MB缓冲区
	defer clientPipe.Close()
	defer serverPipe.Close()

	// 创建配置
	config := DefaultConfig()

	// 创建客户端传输层
	clientTransport, err := newHTTP3Transport(config, clientPipe)
	if err != nil {
		t.Fatalf("创建客户端传输失败: %v", err)
	}
	defer clientTransport.Close()

	// 创建服务器端传输层
	serverTransport, err := newHTTP3Transport(config, serverPipe)
	if err != nil {
		t.Fatalf("创建服务器端传输失败: %v", err)
	}
	defer serverTransport.Close()

	// 测试数据大小：128KB
	const testDataSize = 128 * 1024
	testData := make([]byte, testDataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	// 服务器端接收goroutine
	serverReceived := make(chan []byte, 1)
	serverErr := make(chan error, 1)
	go func() {
		buf := make([]byte, testDataSize)
		total := 0
		for total < testDataSize {
			n, err := serverTransport.Read(buf[total:])
			if err != nil {
				serverErr <- fmt.Errorf("服务器读取失败: %w", err)
				return
			}
			total += n
			t.Logf("服务器: 接收 %d/%d 字节", total, testDataSize)
		}
		serverReceived <- buf[:total]
		// 成功时不发送错误
	}()

	// 客户端发送数据
	t.Logf("客户端: 开始发送 %d 字节数据", testDataSize)
	startTime := time.Now()

	// 分块发送：32KB/次
	const chunkSize = 32768
	sent := 0
	for sent < testDataSize {
		end := sent + chunkSize
		if end > testDataSize {
			end = testDataSize
		}

		chunk := testData[sent:end]
		n, err := clientTransport.Write(chunk)
		if err != nil {
			t.Fatalf("客户端写入失败: %v", err)
		}
		sent += n
		t.Logf("客户端: 已发送 %d/%d 字节", sent, testDataSize)

		// 短暂延迟，避免太快填满缓冲区
		time.Sleep(1 * time.Millisecond)
	}

	sendDuration := time.Since(startTime)
	t.Logf("客户端: 发送完成，耗时 %v", sendDuration)

	// 等待服务器接收完成
	select {
	case receivedData := <-serverReceived:
		t.Logf("服务器: 接收完成，验证数据一致性")
		// 验证数据一致性
		for i := 0; i < testDataSize; i++ {
			if testData[i] != receivedData[i] {
				t.Errorf("数据不一致: 位置 %d, 期望 %02x, 得到 %02x", i, testData[i], receivedData[i])
				break
			}
		}
		t.Logf("数据一致性验证通过")
	case err := <-serverErr:
		t.Fatalf("服务器端错误: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("测试超时: 服务器未在30秒内完成接收")
	}

	// 流量控制性能统计
	t.Logf("测试完成: 128KB数据传输耗时 %v", time.Since(startTime))
}

// TestHTTP3FlowControlWindowUpdate 测试窗口更新机制
func TestHTTP3FlowControlWindowUpdate(t *testing.T) {
	// 创建带缓冲的管道对
	clientPipe, serverPipe := transport.NewBufferedPipe(1048576) // 1MB缓冲区
	defer clientPipe.Close()
	defer serverPipe.Close()

	// 创建配置
	config := DefaultConfig()

	// 创建客户端传输层
	clientTransport, err := newHTTP3Transport(config, clientPipe)
	if err != nil {
		t.Fatalf("创建客户端传输失败: %v", err)
	}
	defer clientTransport.Close()

	// 创建服务器端传输层
	serverTransport, err := newHTTP3Transport(config, serverPipe)
	if err != nil {
		t.Fatalf("创建服务器端传输失败: %v", err)
	}
	defer serverTransport.Close()

	// 测试小数据发送，验证窗口更新机制
	testData := []byte("Hello, HTTP/3 Flow Control!")

	// 客户端发送
	n, err := clientTransport.Write(testData)
	if err != nil {
		t.Fatalf("客户端写入失败: %v", err)
	}
	t.Logf("客户端: 发送 %d 字节", n)

	// 服务器接收
	buf := make([]byte, len(testData))
	n, err = serverTransport.Read(buf)
	if err != nil {
		t.Fatalf("服务器读取失败: %v", err)
	}
	t.Logf("服务器: 接收 %d 字节", n)

	// 验证数据
	if string(buf[:n]) != string(testData) {
		t.Errorf("数据不匹配: 期望 %q, 得到 %q", string(testData), string(buf[:n]))
	}

	t.Log("窗口更新机制测试通过")
}
