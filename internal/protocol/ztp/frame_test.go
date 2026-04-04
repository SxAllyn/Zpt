package ztp

import (
	"bytes"
	"testing"
)

func TestNewFrame(t *testing.T) {
	payload := []byte("test payload")
	frame := NewFrame(TypeData, 123, payload)

	if frame.Magic != FrameMagic {
		t.Errorf("Magic 错误: 期望 %#x, 得到 %#x", FrameMagic, frame.Magic)
	}
	if frame.Version != ProtocolVersion {
		t.Errorf("Version 错误: 期望 %#x, 得到 %#x", ProtocolVersion, frame.Version)
	}
	if frame.Type != TypeData {
		t.Errorf("Type 错误: 期望 %#x, 得到 %#x", TypeData, frame.Type)
	}
	if frame.StreamID != 123 {
		t.Errorf("StreamID 错误: 期望 %d, 得到 %d", 123, frame.StreamID)
	}
	if frame.Length != uint16(len(payload)) {
		t.Errorf("Length 错误: 期望 %d, 得到 %d", len(payload), frame.Length)
	}
	if !bytes.Equal(frame.Payload, payload) {
		t.Error("Payload 不匹配")
	}
}

func TestFrameFlags(t *testing.T) {
	frame := NewFrame(TypeData, 1, []byte("test"))

	// 测试设置和检查标志
	frame.SetFlag(FlagPriority)
	if !frame.HasFlag(FlagPriority) {
		t.Error("SetFlag 后 HasFlag 应该返回 true")
	}

	frame.ClearFlag(FlagPriority)
	if frame.HasFlag(FlagPriority) {
		t.Error("ClearFlag 后 HasFlag 应该返回 false")
	}

	// 测试多个标志
	frame.SetFlag(FlagPriority)
	frame.SetFlag(FlagSynchronous)
	if !frame.HasFlag(FlagPriority | FlagSynchronous) {
		t.Error("HasFlag 应该检测多个标志")
	}
}

func TestFrame_EncodeDecode(t *testing.T) {
	tests := []struct {
		name    string
		frame   *Frame
		wantErr bool
	}{
		{
			name:    "数据帧",
			frame:   NewFrame(TypeData, 123, []byte("hello world")),
			wantErr: false,
		},
		{
			name:    "打开流帧",
			frame:   NewFrame(TypeStreamOpen, 456, []byte{}),
			wantErr: false,
		},
		{
			name:    "关闭流帧",
			frame:   NewFrame(TypeStreamClose, 789, []byte("close reason")),
			wantErr: false,
		},
		{
			name:    "大流ID",
			frame:   NewFrame(TypeData, MaxStreamID, make([]byte, 100)),
			wantErr: false,
		},
		{
			name: "带标志的帧",
			frame: func() *Frame {
				f := NewFrame(TypeData, 1, []byte("flagged"))
				f.SetFlag(FlagPriority)
				f.SetFlag(FlagFinal)
				return f
			}(),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 编码
			encoded, err := tt.frame.Encode()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Encode() 错误 = %v, 期望错误 %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}

			// 解码
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode() 错误 = %v", err)
			}

			// 比较
			if decoded.Magic != tt.frame.Magic {
				t.Errorf("Magic 不匹配: 期望 %#x, 得到 %#x", tt.frame.Magic, decoded.Magic)
			}
			if decoded.Version != tt.frame.Version {
				t.Errorf("Version 不匹配: 期望 %#x, 得到 %#x", tt.frame.Version, decoded.Version)
			}
			if decoded.Type != tt.frame.Type {
				t.Errorf("Type 不匹配: 期望 %#x, 得到 %#x", tt.frame.Type, decoded.Type)
			}
			if decoded.Flags != tt.frame.Flags {
				t.Errorf("Flags 不匹配: 期望 %#x, 得到 %#x", tt.frame.Flags, decoded.Flags)
			}
			if decoded.StreamID != tt.frame.StreamID {
				t.Errorf("StreamID 不匹配: 期望 %d, 得到 %d", tt.frame.StreamID, decoded.StreamID)
			}
			if decoded.Length != tt.frame.Length {
				t.Errorf("Length 不匹配: 期望 %d, 得到 %d", tt.frame.Length, decoded.Length)
			}
			if !bytes.Equal(decoded.Payload, tt.frame.Payload) {
				t.Error("Payload 不匹配")
			}
		})
	}
}

func TestFrame_Validate(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Frame)
		wantErr bool
	}{
		{
			name:    "有效帧",
			modify:  func(f *Frame) {},
			wantErr: false,
		},
		{
			name: "无效魔数",
			modify: func(f *Frame) {
				f.Magic = 0x00
			},
			wantErr: true,
		},
		{
			name: "无效版本",
			modify: func(f *Frame) {
				f.Version = 0x99
			},
			wantErr: true,
		},
		{
			name: "无效帧类型",
			modify: func(f *Frame) {
				f.Type = 0xFF
			},
			wantErr: true,
		},
		{
			name: "载荷长度不匹配",
			modify: func(f *Frame) {
				f.Length = 100
				f.Payload = make([]byte, 50)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frame := NewFrame(TypeData, 1, []byte("test"))
			tt.modify(frame)

			err := frame.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() 错误 = %v, 期望错误 %v", err, tt.wantErr)
			}
		})
	}
}

func TestVarintEncoding(t *testing.T) {
	tests := []struct {
		value uint32
		bytes int // 预期编码字节数
	}{
		{0, 1},
		{1, 1},
		{127, 1},
		{128, 2},
		{16383, 2},
		{16384, 3},
		{2097151, 3},
		{2097152, 4},
		{268435455, 4},
		{268435456, 5},
	}

	for _, tt := range tests {
		t.Run(string(rune(tt.value)), func(t *testing.T) {
			// 编码
			encoded := encodeVarint(tt.value)
			if len(encoded) != tt.bytes {
				t.Errorf("编码字节数错误: 期望 %d, 得到 %d", tt.bytes, len(encoded))
			}

			// 解码
			decoded, n, err := decodeVarint(encoded)
			if err != nil {
				t.Fatalf("解码错误: %v", err)
			}
			if decoded != tt.value {
				t.Errorf("解码值错误: 期望 %d, 得到 %d", tt.value, decoded)
			}
			if n != tt.bytes {
				t.Errorf("解码字节数错误: 期望 %d, 得到 %d", tt.bytes, n)
			}
		})
	}
}

func TestReadWriteFrame(t *testing.T) {
	// 创建测试帧
	frame := NewFrame(TypeData, 12345, []byte("test read/write"))
	frame.SetFlag(FlagPriority)

	// 创建缓冲区
	var buf bytes.Buffer

	// 写入帧
	if err := WriteFrame(&buf, frame); err != nil {
		t.Fatalf("WriteFrame 错误: %v", err)
	}

	// 读取帧
	readFrame, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame 错误: %v", err)
	}

	// 验证
	if readFrame.StreamID != frame.StreamID {
		t.Errorf("StreamID 不匹配: 期望 %d, 得到 %d", frame.StreamID, readFrame.StreamID)
	}
	if readFrame.Type != frame.Type {
		t.Errorf("Type 不匹配: 期望 %#x, 得到 %#x", frame.Type, readFrame.Type)
	}
	if readFrame.Flags != frame.Flags {
		t.Errorf("Flags 不匹配: 期望 %#x, 得到 %#x", frame.Flags, readFrame.Flags)
	}
	if !bytes.Equal(readFrame.Payload, frame.Payload) {
		t.Error("Payload 不匹配")
	}
}

func TestReadFrame_InvalidData(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"空数据", []byte{}},
		{"头部过短", []byte{0x5A, 0x01}},
		{"无效魔数", []byte{0x00, 0x01, 0x01, 0x00, 0x01, 0x00, 0x00}},
		{"流ID不完整", []byte{0x5A, 0x01, 0x01, 0x00, 0x80}},                  // 只有高字节
		{"长度不完整", []byte{0x5A, 0x01, 0x01, 0x00, 0x01, 0x00}},             // 缺少长度第二字节
		{"载荷不完整", []byte{0x5A, 0x01, 0x01, 0x00, 0x01, 0x02, 0x00, 0x01}}, // 长度2但只有1字节载荷
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadFrame(bytes.NewReader(tt.data))
			if err == nil {
				t.Error("期望错误但得到 nil")
			}
		})
	}
}

func TestFrame_EdgeCases(t *testing.T) {
	// 测试最大帧大小
	largePayload := make([]byte, MaxFrameSize)
	frame := NewFrame(TypeData, 1, largePayload)

	if err := frame.Validate(); err != nil {
		t.Errorf("最大帧验证失败: %v", err)
	}

	encoded, err := frame.Encode()
	if err != nil {
		t.Errorf("最大帧编码失败: %v", err)
	}

	decoded, err := Decode(encoded)
	if err != nil {
		t.Errorf("最大帧解码失败: %v", err)
	}

	if len(decoded.Payload) != MaxFrameSize {
		t.Errorf("最大帧载荷大小错误: 期望 %d, 得到 %d", MaxFrameSize, len(decoded.Payload))
	}

	// 测试零长度载荷
	emptyFrame := NewFrame(TypePing, 1, []byte{})
	if emptyFrame.Length != 0 {
		t.Errorf("空载荷帧长度错误: 期望 0, 得到 %d", emptyFrame.Length)
	}
}

func TestFrame_StreamIDEncoding(t *testing.T) {
	// 测试各种流ID的编码
	streamIDs := []uint32{
		0,
		1,
		127,
		128,
		255,
		256,
		1000,
		10000,
		65535,
		100000,
		1000000,
		MaxStreamID,
	}

	for _, streamID := range streamIDs {
		t.Run(string(rune(streamID)), func(t *testing.T) {
			frame := NewFrame(TypeData, streamID, []byte("test"))

			encoded, err := frame.Encode()
			if err != nil {
				t.Fatalf("编码错误: %v", err)
			}

			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("解码错误: %v", err)
			}

			if decoded.StreamID != streamID {
				t.Errorf("流ID不匹配: 期望 %d, 得到 %d", streamID, decoded.StreamID)
			}
		})
	}
}

func BenchmarkFrameEncode(b *testing.B) {
	frame := NewFrame(TypeData, 12345, make([]byte, 1024))

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = frame.Encode()
	}
}

func BenchmarkFrameDecode(b *testing.B) {
	frame := NewFrame(TypeData, 12345, make([]byte, 1024))
	encoded, _ := frame.Encode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = Decode(encoded)
	}
}

func TestAckPayloadEncoding(t *testing.T) {
	tests := []struct {
		name       string
		ackedBytes uint32
		windowSize uint32
		wantErr    bool
	}{
		{"正常ACK", 1024, 65535, false},
		{"零确认", 0, 65535, false},
		{"零窗口", 1024, 0, false},
		{"全零", 0, 0, false},
		{"最大值", 0xFFFFFFFF, 0xFFFFFFFF, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// 编码
			encoded := EncodeAckPayload(tt.ackedBytes, tt.windowSize)

			// 验证长度
			if len(encoded) != 8 {
				t.Errorf("编码长度错误: 期望 8, 得到 %d", len(encoded))
			}

			// 解码
			decoded, err := DecodeAckPayload(encoded)
			if (err != nil) != tt.wantErr {
				t.Errorf("DecodeAckPayload() 错误 = %v, 期望错误 = %v", err, tt.wantErr)
				return
			}

			if decoded.AckedBytes != tt.ackedBytes {
				t.Errorf("解码后AckedBytes不匹配: 期望 %d, 得到 %d", tt.ackedBytes, decoded.AckedBytes)
			}

			if decoded.WindowSize != tt.windowSize {
				t.Errorf("解码后WindowSize不匹配: 期望 %d, 得到 %d", tt.windowSize, decoded.WindowSize)
			}
		})
	}
}

func TestDecodeAckPayload_InvalidLength(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"空数据", []byte{}},
		{"过短", []byte{0, 0, 0, 0}},
		{"过长", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeAckPayload(tt.data)
			if err == nil {
				t.Error("期望错误但得到nil")
			}
		})
	}
}

func TestNewAckFrame(t *testing.T) {
	streamID := uint32(123)
	ackedBytes := uint32(1024)
	windowSize := uint32(65535)

	frame := NewAckFrame(streamID, ackedBytes, windowSize)

	// 验证帧基本属性
	if frame.Magic != FrameMagic {
		t.Errorf("Magic错误: 期望 %#x, 得到 %#x", FrameMagic, frame.Magic)
	}

	if frame.Version != ProtocolVersion {
		t.Errorf("Version错误: 期望 %#x, 得到 %#x", ProtocolVersion, frame.Version)
	}

	if frame.Type != TypeAck {
		t.Errorf("Type错误: 期望 %#x, 得到 %#x", TypeAck, frame.Type)
	}

	if frame.StreamID != streamID {
		t.Errorf("StreamID错误: 期望 %d, 得到 %d", streamID, frame.StreamID)
	}

	// 验证载荷
	if len(frame.Payload) != 8 {
		t.Errorf("Payload长度错误: 期望 8, 得到 %d", len(frame.Payload))
	}

	// 解码验证内容
	ack, err := DecodeAckPayload(frame.Payload)
	if err != nil {
		t.Fatalf("解码ACK载荷失败: %v", err)
	}

	if ack.AckedBytes != ackedBytes {
		t.Errorf("载荷AckedBytes不匹配: 期望 %d, 得到 %d", ackedBytes, ack.AckedBytes)
	}

	if ack.WindowSize != windowSize {
		t.Errorf("载荷WindowSize不匹配: 期望 %d, 得到 %d", windowSize, ack.WindowSize)
	}
}
