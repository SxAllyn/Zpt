// Package ztp 实现 Ztp 隧道协议
package ztp

import (
	"encoding/binary"
	"errors"
	"io"
)

// 常量定义
const (
	// FrameMagic 帧魔数
	FrameMagic = 0x5A // 字母 'Z'

	// ProtocolVersion 协议版本
	ProtocolVersion = 0x01

	// MaxFrameSize 最大帧大小
	MaxFrameSize = 65535

	// MaxStreamID 最大流ID
	MaxStreamID = 1<<32 - 1
)

// FrameType 帧类型
type FrameType uint8

const (
	// TypeData 数据帧
	TypeData FrameType = 0x1

	// TypeStreamOpen 打开流帧
	TypeStreamOpen FrameType = 0x2

	// TypeStreamClose 关闭流帧
	TypeStreamClose FrameType = 0x3

	// TypeAck 确认帧
	TypeAck FrameType = 0x4

	// TypePing Ping帧
	TypePing FrameType = 0x5

	// TypeReset 重置帧
	TypeReset FrameType = 0x6
)

// FrameFlags 帧标志位
type FrameFlags uint8

const (
	// FlagPriority 优先级标志（高优先级）
	FlagPriority FrameFlags = 1 << 0

	// FlagSynchronous 同步标志
	FlagSynchronous FrameFlags = 1 << 1

	// FlagFinal 最后帧标志
	FlagFinal FrameFlags = 1 << 2
)

// Frame Ztp帧结构
type Frame struct {
	// 头部
	Magic   uint8      // 魔数 0x5A
	Version uint8      // 版本号
	Type    FrameType  // 帧类型
	Flags   FrameFlags // 标志位

	// 流ID（变长编码）
	StreamID uint32

	// 载荷长度
	Length uint16

	// 载荷数据
	Payload []byte
}

// NewFrame 创建新帧
func NewFrame(frameType FrameType, streamID uint32, payload []byte) *Frame {
	return &Frame{
		Magic:    FrameMagic,
		Version:  ProtocolVersion,
		Type:     frameType,
		Flags:    0,
		StreamID: streamID,
		Length:   uint16(len(payload)),
		Payload:  payload,
	}
}

// SetFlag 设置标志位
func (f *Frame) SetFlag(flag FrameFlags) {
	f.Flags |= flag
}

// ClearFlag 清除标志位
func (f *Frame) ClearFlag(flag FrameFlags) {
	f.Flags &^= flag
}

// HasFlag 检查是否有标志位
func (f *Frame) HasFlag(flag FrameFlags) bool {
	return f.Flags&flag != 0
}

// Encode 编码帧为字节流
func (f *Frame) Encode() ([]byte, error) {
	// 验证帧数据
	if err := f.Validate(); err != nil {
		return nil, err
	}

	// 计算流ID编码长度
	streamIDBytes := encodeVarint(f.StreamID)

	// 计算总长度
	totalLen := 4 + len(streamIDBytes) + 2 + len(f.Payload) // 头部4字节 + 流ID + 长度2字节 + 载荷

	// 创建缓冲区
	buf := make([]byte, totalLen)

	// 写入头部
	buf[0] = f.Magic
	buf[1] = f.Version
	buf[2] = uint8(f.Type)
	buf[3] = uint8(f.Flags)

	// 写入流ID（变长编码）
	copy(buf[4:], streamIDBytes)
	offset := 4 + len(streamIDBytes)

	// 写入长度
	binary.LittleEndian.PutUint16(buf[offset:], f.Length)
	offset += 2

	// 写入载荷
	copy(buf[offset:], f.Payload)

	return buf, nil
}

// Decode 从字节流解码帧
func Decode(data []byte) (*Frame, error) {
	if len(data) < 6 { // 最小帧大小：头部4字节 + 流ID至少1字节 + 长度2字节
		return nil, errors.New("帧数据过短")
	}

	// 检查魔数
	if data[0] != FrameMagic {
		return nil, errors.New("无效的魔数")
	}

	// 读取头部
	frame := &Frame{
		Magic:   data[0],
		Version: data[1],
		Type:    FrameType(data[2]),
		Flags:   FrameFlags(data[3]),
	}

	// 解码流ID（变长编码）
	streamID, n, err := decodeVarint(data[4:])
	if err != nil {
		return nil, err
	}
	if streamID > MaxStreamID {
		return nil, errors.New("流ID超出范围")
	}
	frame.StreamID = streamID

	// 读取长度
	lengthStart := 4 + n
	if lengthStart+2 > len(data) {
		return nil, errors.New("帧数据不完整")
	}
	frame.Length = binary.LittleEndian.Uint16(data[lengthStart:])

	// 读取载荷
	payloadStart := lengthStart + 2
	if payloadStart+int(frame.Length) > len(data) {
		return nil, errors.New("载荷长度超过帧数据")
	}
	frame.Payload = make([]byte, frame.Length)
	copy(frame.Payload, data[payloadStart:payloadStart+int(frame.Length)])

	// 验证帧
	if err := frame.Validate(); err != nil {
		return nil, err
	}

	return frame, nil
}

// Validate 验证帧数据
func (f *Frame) Validate() error {
	if f.Magic != FrameMagic {
		return errors.New("无效的魔数")
	}

	if f.Version != ProtocolVersion {
		return errors.New("不支持的协议版本")
	}

	if f.Type < TypeData || f.Type > TypeReset {
		return errors.New("无效的帧类型")
	}

	if f.StreamID > MaxStreamID {
		return errors.New("流ID超出范围")
	}

	if f.Length > MaxFrameSize {
		return errors.New("载荷长度超出限制")
	}

	if len(f.Payload) != int(f.Length) {
		return errors.New("载荷长度不匹配")
	}

	return nil
}

// encodeVarint 编码变长整数（LEB128）
func encodeVarint(value uint32) []byte {
	if value == 0 {
		return []byte{0}
	}

	var buf []byte
	for value > 0 {
		b := value & 0x7F
		value >>= 7
		if value > 0 {
			b |= 0x80
		}
		buf = append(buf, byte(b))
	}
	return buf
}

// decodeVarint 解码变长整数（LEB128）
func decodeVarint(data []byte) (uint32, int, error) {
	var result uint32
	var shift uint
	var bytesRead int

	for _, b := range data {
		bytesRead++
		result |= uint32(b&0x7F) << shift

		if b&0x80 == 0 {
			break
		}

		shift += 7
		if shift >= 32 {
			return 0, 0, errors.New("变长整数溢出")
		}
	}

	if bytesRead == 0 {
		return 0, 0, errors.New("无变长整数数据")
	}

	return result, bytesRead, nil
}

// ReadFrame 从Reader读取帧
func ReadFrame(r io.Reader) (*Frame, error) {
	// 先读取头部（4字节）
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	// 检查魔数
	if header[0] != FrameMagic {
		return nil, errors.New("无效的魔数")
	}

	// 读取流ID（变长编码）
	var streamID uint32
	var streamIDBytes []byte
	temp := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, temp); err != nil {
			return nil, err
		}
		streamIDBytes = append(streamIDBytes, temp[0])

		if temp[0]&0x80 == 0 {
			// 解码流ID
			var err error
			streamID, _, err = decodeVarint(streamIDBytes)
			if err != nil {
				return nil, err
			}
			break
		}
	}

	// 读取长度（2字节）
	lengthBytes := make([]byte, 2)
	if _, err := io.ReadFull(r, lengthBytes); err != nil {
		return nil, err
	}
	length := binary.LittleEndian.Uint16(lengthBytes)

	// 读取载荷
	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	// 构建帧
	frame := &Frame{
		Magic:    header[0],
		Version:  header[1],
		Type:     FrameType(header[2]),
		Flags:    FrameFlags(header[3]),
		StreamID: streamID,
		Length:   length,
		Payload:  payload,
	}

	// 验证帧
	if err := frame.Validate(); err != nil {
		return nil, err
	}

	return frame, nil
}

// WriteFrame 将帧写入Writer
func WriteFrame(w io.Writer, frame *Frame) error {
	data, err := frame.Encode()
	if err != nil {
		return err
	}

	_, err = w.Write(data)
	return err
}
