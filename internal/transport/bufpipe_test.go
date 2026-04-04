// Package transport 提供传输层实现
package transport

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// 确保 net 包被使用（用于解决编译器检测问题）
var _ net.Addr = (*net.TCPAddr)(nil)

func TestBufferedPipe_Create(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	if client == nil || server == nil {
		t.Fatal("NewBufferedPipe 返回 nil 连接")
	}
	defer client.Close()
	defer server.Close()

	// 测试地址信息
	if client.LocalAddr() == nil {
		t.Error("客户端 LocalAddr 不应为 nil")
	}
	if client.RemoteAddr() == nil {
		t.Error("客户端 RemoteAddr 不应为 nil")
	}
	if server.LocalAddr() == nil {
		t.Error("服务端 LocalAddr 不应为 nil")
	}
	if server.RemoteAddr() == nil {
		t.Error("服务端 RemoteAddr 不应为 nil")
	}
}

func TestBufferedPipe_BasicIO(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	defer client.Close()
	defer server.Close()

	// 客户端写入，服务端读取
	testData := []byte("hello buffered pipe")
	go func() {
		n, err := client.Write(testData)
		if err != nil {
			panic("客户端写入失败: " + err.Error())
		}
		if n != len(testData) {
			panic("客户端写入字节数不匹配")
		}
		client.Close()
	}()

	buf := make([]byte, len(testData))
	n, err := io.ReadFull(server, buf)
	if err != nil {
		t.Fatalf("服务端读取失败: %v", err)
	}
	if n != len(testData) {
		t.Fatalf("服务端读取字节数不匹配: 期望 %d, 实际 %d", len(testData), n)
	}
	if string(buf) != string(testData) {
		t.Fatalf("数据不匹配: 期望 %q, 实际 %q", testData, buf)
	}
}

func TestBufferedPipe_Bidirectional(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	defer client.Close()
	defer server.Close()

	// 双向通信测试
	clientData := []byte("from client")
	serverData := []byte("from server")

	var wg sync.WaitGroup
	wg.Add(2)

	// 客户端写入，服务端读取
	go func() {
		defer wg.Done()
		n, err := client.Write(clientData)
		if err != nil {
			t.Errorf("客户端写入失败: %v", err)
			return
		}
		if n != len(clientData) {
			t.Errorf("客户端写入字节数不匹配: 期望 %d, 实际 %d", len(clientData), n)
		}
	}()

	// 服务端写入，客户端读取
	go func() {
		defer wg.Done()
		n, err := server.Write(serverData)
		if err != nil {
			t.Errorf("服务端写入失败: %v", err)
			return
		}
		if n != len(serverData) {
			t.Errorf("服务端写入字节数不匹配: 期望 %d, 实际 %d", len(serverData), n)
		}
	}()

	// 客户端读取服务端数据
	clientBuf := make([]byte, len(serverData))
	n, err := io.ReadFull(client, clientBuf)
	if err != nil {
		t.Fatalf("客户端读取失败: %v", err)
	}
	if n != len(serverData) {
		t.Fatalf("客户端读取字节数不匹配: 期望 %d, 实际 %d", len(serverData), n)
	}
	if string(clientBuf) != string(serverData) {
		t.Fatalf("客户端数据不匹配: 期望 %q, 实际 %q", serverData, clientBuf)
	}

	// 服务端读取客户端数据
	serverBuf := make([]byte, len(clientData))
	n, err = io.ReadFull(server, serverBuf)
	if err != nil {
		t.Fatalf("服务端读取失败: %v", err)
	}
	if n != len(clientData) {
		t.Fatalf("服务端读取字节数不匹配: 期望 %d, 实际 %d", len(clientData), n)
	}
	if string(serverBuf) != string(clientData) {
		t.Fatalf("服务端数据不匹配: 期望 %q, 实际 %q", clientData, serverBuf)
	}

	wg.Wait()
}

func TestBufferedPipe_BufferLimit(t *testing.T) {
	// 使用小缓冲区测试限制
	client, server := NewBufferedPipe(100) // 100字节缓冲区
	defer client.Close()
	defer server.Close()

	// 写入超过缓冲区大小的数据（分两次）
	data := make([]byte, 150)
	for i := range data {
		data[i] = byte(i % 256)
	}

	// 启动读取goroutine（稍后读取，模拟慢消费者）
	readDone := make(chan struct{})
	go func() {
		time.Sleep(100 * time.Millisecond) // 延迟读取
		buf := make([]byte, 50)
		n, err := server.Read(buf)
		if err != nil {
			t.Errorf("延迟读取失败: %v", err)
		}
		t.Logf("延迟读取了 %d 字节", n)
		close(readDone)
	}()

	// 写入数据（应能成功写入至少缓冲区大小的数据）
	n, err := client.Write(data[:75]) // 写入75字节，小于缓冲区大小
	if err != nil {
		t.Fatalf("第一次写入失败: %v", err)
	}
	if n != 75 {
		t.Fatalf("第一次写入字节数不匹配: 期望 75, 实际 %d", n)
	}

	// 等待读取完成一些数据
	<-readDone

	// 继续写入剩余数据
	n, err = client.Write(data[75:])
	if err != nil {
		t.Fatalf("第二次写入失败: %v", err)
	}
	if n != 75 {
		t.Fatalf("第二次写入字节数不匹配: 期望 75, 实际 %d", n)
	}
}

func TestBufferedPipe_Concurrent(t *testing.T) {
	client, server := NewBufferedPipe(1024 * 1024) // 1MB缓冲区
	defer client.Close()
	defer server.Close()

	const numGoroutines = 10
	const messagesPerGoroutine = 100
	const messageSize = 1024 // 1KB每条消息

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2)

	// 启动多个写入goroutine
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			data := make([]byte, messageSize)
			for j := 0; j < messagesPerGoroutine; j++ {
				// 填充唯一数据
				data[0] = byte(id)
				data[1] = byte(j)
				_, err := client.Write(data)
				if err != nil {
					t.Errorf("goroutine %d 写入失败: %v", id, err)
					return
				}
			}
		}(i)
	}

	// 启动多个读取goroutine
	totalRead := 0
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			buf := make([]byte, messageSize)
			for j := 0; j < messagesPerGoroutine; j++ {
				n, err := io.ReadFull(server, buf)
				if err != nil {
					t.Errorf("goroutine %d 读取失败: %v", id, err)
					return
				}
				totalRead += n
			}
		}(i)
	}

	wg.Wait()

	expectedTotal := numGoroutines * messagesPerGoroutine * messageSize
	if totalRead != expectedTotal {
		t.Errorf("总读取字节数不匹配: 期望 %d, 实际 %d", expectedTotal, totalRead)
	}
}

func TestBufferedPipe_Deadline(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	defer client.Close()
	defer server.Close()

	// 测试读取超时（缓冲区为空）
	server.SetReadDeadline(time.Now().Add(50 * time.Millisecond))

	buf := make([]byte, 10)
	start := time.Now()
	n, err := server.Read(buf)
	elapsed := time.Since(start)

	if n != 0 {
		t.Errorf("超时读取应返回 0 字节，实际返回 %d", n)
	}
	if err == nil {
		t.Error("超时读取应返回错误")
	}
	if elapsed < 45*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("超时时间异常: %v", elapsed)
	}

	// 测试写入超时（缓冲区满）
	// 首先填满缓冲区
	smallClient, smallServer := NewBufferedPipe(100) // 小缓冲区
	defer smallClient.Close()
	defer smallServer.Close()

	// 写入数据填满缓冲区（不读取）
	fillData := make([]byte, 100)
	n, err = smallClient.Write(fillData)
	if err != nil {
		t.Fatalf("填满缓冲区失败: %v", err)
	}
	if n != 100 {
		t.Fatalf("填满缓冲区字节数不匹配: 期望 100, 实际 %d", n)
	}

	// 设置写入超时
	smallClient.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))

	extraData := []byte("extra")
	start = time.Now()
	n, err = smallClient.Write(extraData)
	elapsed = time.Since(start)

	if n != 0 {
		t.Errorf("超时写入应返回 0 字节，实际返回 %d", n)
	}
	if err == nil {
		t.Error("超时写入应返回错误")
	}
	if elapsed < 45*time.Millisecond || elapsed > 200*time.Millisecond {
		t.Errorf("超时时间异常: %v", elapsed)
	}
}

func TestBufferedPipe_TryReadTryWrite(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	defer client.Close()
	defer server.Close()

	// 测试 TryRead（缓冲区为空）
	buf := make([]byte, 10)
	n, err := server.(*BufferedPipe).TryRead(buf)
	if err != ErrBufferEmpty {
		t.Errorf("TryRead 空缓冲区应返回 ErrBufferEmpty，实际: %v", err)
	}
	if n != 0 {
		t.Errorf("TryRead 空缓冲区应返回 0 字节，实际: %d", n)
	}

	// 测试 TryWrite
	testData := []byte("test")
	n, err = client.(*BufferedPipe).TryWrite(testData)
	if err != nil {
		t.Errorf("TryWrite 失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("TryWrite 字节数不匹配: 期望 %d, 实际 %d", len(testData), n)
	}

	// 现在 TryRead 应有数据
	n, err = server.(*BufferedPipe).TryRead(buf)
	if err != nil {
		t.Errorf("TryRead 有数据时失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("TryRead 字节数不匹配: 期望 %d, 实际 %d", len(testData), n)
	}
	if string(buf[:n]) != string(testData) {
		t.Errorf("TryRead 数据不匹配: 期望 %q, 实际 %q", testData, buf[:n])
	}
}

func TestBufferedPipe_LargeData(t *testing.T) {
	// 测试大数据传输（类似压力测试场景）
	client, server := NewBufferedPipe(1 * 1024 * 1024) // 1MB缓冲区
	defer client.Close()
	defer server.Close()

	const dataSize = 10 * 1024 * 1024 // 10MB测试数据
	testData := make([]byte, dataSize)
	for i := range testData {
		testData[i] = byte(i % 256)
	}

	var wg sync.WaitGroup
	wg.Add(2)

	// 发送方
	start := time.Now()
	go func() {
		defer wg.Done()
		totalSent := 0
		chunkSize := 64 * 1024 // 64KB分块
		for totalSent < dataSize {
			end := totalSent + chunkSize
			if end > dataSize {
				end = dataSize
			}
			chunk := testData[totalSent:end]
			n, err := client.Write(chunk)
			if err != nil {
				t.Errorf("发送数据失败: %v", err)
				return
			}
			totalSent += n
		}
		t.Logf("发送完成: %d 字节, 耗时 %v", totalSent, time.Since(start))
	}()

	// 接收方
	receivedData := make([]byte, dataSize)
	totalReceived := 0
	go func() {
		defer wg.Done()
		for totalReceived < dataSize {
			n, err := server.Read(receivedData[totalReceived:])
			if err != nil {
				t.Errorf("接收数据失败: %v", err)
				return
			}
			totalReceived += n
		}
		t.Logf("接收完成: %d 字节, 耗时 %v", totalReceived, time.Since(start))
	}()

	wg.Wait()

	// 验证数据
	if totalReceived != dataSize {
		t.Fatalf("接收数据大小不匹配: 期望 %d, 实际 %d", dataSize, totalReceived)
	}
	for i := 0; i < dataSize; i++ {
		if testData[i] != receivedData[i] {
			t.Fatalf("数据不一致: 位置 %d, 期望 %02x, 实际 %02x", i, testData[i], receivedData[i])
		}
	}
	t.Logf("大数据传输测试通过: 成功传输 %d 字节", dataSize)
}

func TestBufferedPipe_CloseWhileReading(t *testing.T) {
	client, server := NewBufferedPipe(1024)
	defer client.Close()

	// 启动读取goroutine（将阻塞等待数据）
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 10)
		n, err := server.Read(buf)
		t.Logf("读取结果: n=%d, err=%v", n, err)
		if err != io.EOF {
			t.Errorf("关闭后读取应返回 EOF，实际: %v", err)
		}
		close(readDone)
	}()

	// 给读取goroutine时间开始阻塞
	time.Sleep(50 * time.Millisecond)

	// 关闭服务端连接
	server.Close()

	// 等待读取完成
	select {
	case <-readDone:
		// 正常
	case <-time.After(100 * time.Millisecond):
		t.Error("读取goroutine在关闭后未返回")
	}
}

func TestBufferedPipe_CloseWhileWriting(t *testing.T) {
	client, server := NewBufferedPipe(100) // 小缓冲区
	defer server.Close()

	// 填满缓冲区
	fillData := make([]byte, 100)
	n, err := client.Write(fillData)
	if err != nil {
		t.Fatalf("填满缓冲区失败: %v", err)
	}
	if n != 100 {
		t.Fatalf("填满缓冲区字节数不匹配: 期望 100, 实际 %d", n)
	}

	// 启动写入goroutine（将阻塞等待缓冲区空间）
	writeDone := make(chan struct{})
	go func() {
		extraData := []byte("extra")
		n, err := client.Write(extraData)
		t.Logf("写入结果: n=%d, err=%v", n, err)
		if err == nil {
			t.Error("关闭后写入应返回错误")
		}
		close(writeDone)
	}()

	// 给写入goroutine时间开始阻塞
	time.Sleep(50 * time.Millisecond)

	// 关闭客户端连接
	client.Close()

	// 等待写入完成
	select {
	case <-writeDone:
		// 正常
	case <-time.After(100 * time.Millisecond):
		t.Error("写入goroutine在关闭后未返回")
	}
}
