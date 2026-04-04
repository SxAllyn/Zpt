package ztp

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// testTransport 用于测试的简单传输实现
type testTransport struct {
	clientConn net.Conn // 客户端连接（用于读写）
	serverConn net.Conn // 服务器端连接（仅用于保持管道打开）
}

func newTestTransport() *testTransport {
	client, server := net.Pipe()

	// 启动一个goroutine从服务器端读取并丢弃数据，防止写入阻塞
	go func() {
		buf := make([]byte, 4096)
		for {
			_, err := server.Read(buf)
			if err != nil {
				// 连接关闭或错误，退出
				return
			}
			// 丢弃数据
		}
	}()

	return &testTransport{
		clientConn: client,
		serverConn: server,
	}
}

func (t *testTransport) Read(p []byte) (n int, err error) {
	return t.clientConn.Read(p)
}

func (t *testTransport) Write(p []byte) (n int, err error) {
	return t.clientConn.Write(p)
}

func (t *testTransport) Close() error {
	// 关闭两个连接
	err1 := t.clientConn.Close()
	err2 := t.serverConn.Close()
	if err1 != nil {
		return err1
	}
	return err2
}

// SetReadDeadline 实现 SetReadDeadline 方法，支持超时
func (t *testTransport) SetReadDeadline(deadline time.Time) error {
	return t.clientConn.SetReadDeadline(deadline)
}

// SetWriteDeadline 实现 SetWriteDeadline 方法
func (t *testTransport) SetWriteDeadline(deadline time.Time) error {
	return t.clientConn.SetWriteDeadline(deadline)
}

// SetDeadline 实现 SetDeadline 方法
func (t *testTransport) SetDeadline(deadline time.Time) error {
	return t.clientConn.SetDeadline(deadline)
}

func TestNewSession(t *testing.T) {
	transport := newTestTransport()
	defer transport.Close()

	config := DefaultSessionConfig()
	session, err := NewSession(transport, config)
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	defer session.Close()

	if session == nil {
		t.Fatal("会话为nil")
	}

	// 验证默认配置
	if session.config.MaxStreams != 65535 {
		t.Errorf("MaxStreams配置错误: 期望 65535, 得到 %d", session.config.MaxStreams)
	}

	if session.config.MaxFrameSize != 65535 {
		t.Errorf("MaxFrameSize配置错误: 期望 65535, 得到 %d", session.config.MaxFrameSize)
	}
}

func TestSession_OpenStream(t *testing.T) {
	transport := newTestTransport()
	defer transport.Close()

	session, err := NewSession(transport, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	defer session.Close()

	// 启动会话
	if err := session.Start(); err != nil {
		t.Fatalf("启动会话失败: %v", err)
	}

	// 打开流
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}

	if stream == nil {
		t.Fatal("流为nil")
	}

	// 验证流ID为奇数（客户端发起）
	if stream.ID()%2 != 1 {
		t.Errorf("客户端发起的流ID应为奇数: %d", stream.ID())
	}

	// 验证流为本地发起
	if !stream.LocalInit() {
		t.Error("本地发起的流应标记为LocalInit")
	}

	// 关闭流
	if err := stream.Close(); err != nil {
		t.Errorf("关闭流失败: %v", err)
	}
}

func TestSession_StreamFlowControl(t *testing.T) {
	// 使用net.Pipe创建双向传输
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// 客户端会话
	clientSession, err := NewSession(clientConn, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建客户端会话失败: %v", err)
	}
	defer clientSession.Close()

	// 服务端会话（简化处理，仅用于接收）
	serverSession, err := NewSession(serverConn, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建服务端会话失败: %v", err)
	}
	defer serverSession.Close()

	// 启动会话
	if err := clientSession.Start(); err != nil {
		t.Fatalf("启动客户端会话失败: %v", err)
	}

	if err := serverSession.Start(); err != nil {
		t.Fatalf("启动服务端会话失败: %v", err)
	}

	// 打开流
	stream, err := clientSession.OpenStream()
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}

	// 测试流控窗口初始化
	// 注意：windowSize字段是私有的，我们通过Write行为来验证

	// 设置较短的超时以便测试
	stream.SetWriteTimeout(100 * time.Millisecond)

	// 第一次写入应该成功（窗口足够）
	testData := []byte("test data")
	n, err := stream.Write(testData)
	if err != nil {
		t.Errorf("第一次写入失败: %v", err)
	}
	if n != len(testData) {
		t.Errorf("写入字节数不匹配: 期望 %d, 得到 %d", len(testData), n)
	}

	// 记录发送字节数（通过反射或其他方式，这里简化处理）
	// 在实际测试中，我们可以模拟ACK来验证流控
	t.Logf("测试数据写入成功: %d 字节", n)
}

func TestSession_HandleAckFrame(t *testing.T) {
	transport := newTestTransport()
	defer transport.Close()

	session, err := NewSession(transport, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	defer session.Close()

	// 启动会话
	if err := session.Start(); err != nil {
		t.Fatalf("启动会话失败: %v", err)
	}

	// 打开一个流
	stream, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开流失败: %v", err)
	}

	// 模拟发送一些数据
	testData := []byte("test")
	n, err := stream.Write(testData)
	if err != nil {
		t.Fatalf("写入数据失败: %v", err)
	}
	t.Logf("已发送 %d 字节数据", n)

	// 创建ACK帧
	ackedBytes := uint32(len(testData))
	windowSize := uint32(50000) // 模拟窗口减小

	ackFrame := NewAckFrame(stream.ID(), ackedBytes, windowSize)

	// 手动调用handleAckFrame（在真实场景中，这由processLoop调用）
	// 这里我们直接调用以测试处理逻辑
	session.handleAckFrame(ackFrame)

	// 验证流控状态已更新
	// 注意：由于字段私有，我们无法直接验证，但可以通过后续Write行为验证
	// 在真实测试中，可能需要添加getter方法或使用反射

	t.Log("ACK帧处理测试完成")
}

func TestSession_IsClosed(t *testing.T) {
	transport := newTestTransport()
	defer transport.Close()

	session, err := NewSession(transport, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}

	// 初始状态应为未关闭
	if session.IsClosed() {
		t.Error("新会话不应标记为已关闭")
	}

	// 关闭会话
	if err := session.Close(); err != nil {
		t.Errorf("关闭会话失败: %v", err)
	}

	// 关闭后应标记为已关闭
	if !session.IsClosed() {
		t.Error("关闭后会话应标记为已关闭")
	}

	// 重复关闭不应出错
	if err := session.Close(); err != nil {
		t.Errorf("重复关闭失败: %v", err)
	}
}

func TestSession_StreamCount(t *testing.T) {
	transport := newTestTransport()
	defer transport.Close()

	session, err := NewSession(transport, DefaultSessionConfig())
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	defer session.Close()

	// 启动会话
	if err := session.Start(); err != nil {
		t.Fatalf("启动会话失败: %v", err)
	}

	// 初始流数应为0
	if count := session.StreamCount(); count != 0 {
		t.Errorf("初始流数应为0, 得到 %d", count)
	}

	// 打开一个流
	stream1, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开第一个流失败: %v", err)
	}

	// 流数应为1
	if count := session.StreamCount(); count != 1 {
		t.Errorf("打开一个流后流数应为1, 得到 %d", count)
	}

	// 打开第二个流
	stream2, err := session.OpenStream()
	if err != nil {
		t.Fatalf("打开第二个流失败: %v", err)
	}

	// 流数应为2
	if count := session.StreamCount(); count != 2 {
		t.Errorf("打开两个流后流数应为2, 得到 %d", count)
	}

	// 关闭一个流
	stream1.Close()

	// 等待一下让关闭处理完成
	time.Sleep(50 * time.Millisecond)

	// 流数应为1
	if count := session.StreamCount(); count != 1 {
		t.Errorf("关闭一个流后流数应为1, 得到 %d", count)
	}

	// 关闭第二个流
	stream2.Close()

	// 等待一下让关闭处理完成
	time.Sleep(50 * time.Millisecond)

	// 流数应为0
	if count := session.StreamCount(); count != 0 {
		t.Errorf("关闭所有流后流数应为0, 得到 %d", count)
	}
}

// func TestSession_WriteWithFlowControl(t *testing.T) {
// 	// 创建内存缓冲区作为传输层
// 	buf := &bytes.Buffer{}
//
// 	// 创建会话
// 	session, err := NewSession(buf, DefaultSessionConfig())
// 	if err != nil {
// 		t.Fatalf("创建会话失败: %v", err)
// 	}
// 	defer session.Close()
//
// 	// 注意：由于我们使用bytes.Buffer，无法同时读写，这只是一个简化测试
// 	// 实际测试应该使用net.Pipe或自定义双向传输
//
// 	t.Log("流控写入测试框架已建立")
// }

// TestFrameRoundTrip 测试帧的完整往返流程
func TestFrameRoundTrip(t *testing.T) {
	// 创建双向管道
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	// 客户端写入
	go func() {
		frame := NewFrame(TypeData, 1, []byte("hello"))
		if err := WriteFrame(clientConn, frame); err != nil {
			t.Errorf("客户端写入帧失败: %v", err)
		}
		clientConn.Close()
	}()

	// 服务端读取
	frame, err := ReadFrame(serverConn)
	if err != nil && err != io.EOF {
		t.Fatalf("服务端读取帧失败: %v", err)
	}

	if frame == nil {
		t.Fatal("读取的帧为nil")
	}

	if frame.Type != TypeData {
		t.Errorf("帧类型错误: 期望 %#x, 得到 %#x", TypeData, frame.Type)
	}

	if frame.StreamID != 1 {
		t.Errorf("流ID错误: 期望 1, 得到 %d", frame.StreamID)
	}

	if string(frame.Payload) != "hello" {
		t.Errorf("载荷错误: 期望 'hello', 得到 %q", string(frame.Payload))
	}
}

// TestFlowControlWindow 测试流控窗口更新
func TestFlowControlWindow(t *testing.T) {
	// 创建模拟会话
	session := &Session{
		config:           DefaultSessionConfig(),
		streams:          make(map[uint32]*Stream),
		streamsMu:        sync.RWMutex{},
		highPriorityCh:   make(chan *Frame, 10),
		normalPriorityCh: make(chan *Frame, 10),
		lowPriorityCh:    make(chan *Frame, 10),
		recvCh:           make(chan *Frame, 10),
		errors:           make(chan error, 10),
		closeCh:          make(chan struct{}),
		isClosed:         atomic.Bool{},
		isStarted:        atomic.Bool{},
		ctx:              context.Background(),
		cancel:           func() {},
		wg:               sync.WaitGroup{},
	}

	// 创建一个流
	streamID := uint32(1)
	stream := &Stream{
		id:           streamID,
		session:      session,
		recvCh:       make(chan []byte, 10),
		closeCh:      make(chan struct{}),
		isClosed:     atomic.Bool{},
		isLocalInit:  true,
		writeTimeout: 30 * time.Second,
		readTimeout:  30 * time.Second,
	}
	stream.windowSize.Store(65535) // 初始窗口大小

	// 注册流
	session.streams[streamID] = stream

	// 模拟接收ACK帧，确认1000字节，窗口大小更新为50000
	ackPayload := EncodeAckPayload(1000, 50000)
	ackFrame := NewFrame(TypeAck, streamID, ackPayload)
	ackFrame.SetFlag(FlagPriority) // ACK高优先级

	// 处理ACK帧
	session.handleAckFrame(ackFrame)

	// 验证窗口大小已更新
	if stream.windowSize.Load() != 50000 {
		t.Errorf("窗口大小更新错误: 期望 50000, 得到 %d", stream.windowSize.Load())
	}

	// 验证已确认字节数
	if stream.ackedBytes.Load() != 1000 {
		t.Errorf("已确认字节数错误: 期望 1000, 得到 %d", stream.ackedBytes.Load())
	}
}
