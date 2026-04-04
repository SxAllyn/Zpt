// Package zap 实现 Zpt 认证协议
package zap

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// FrameHeaderSize 帧头大小
	FrameHeaderSize = 12 // Magic(4) + Version(1) + Type(1) + Flags(1) + Reserved(1) + Length(4)
)

// FrameType 帧类型
type FrameType uint8

const (
	// TypeClientHello 客户端Hello帧
	TypeClientHello FrameType = 0x01
	// TypeServerChallenge 服务端Challenge帧
	TypeServerChallenge FrameType = 0x02
	// TypeClientResponse 客户端Response帧
	TypeClientResponse FrameType = 0x03
	// TypeServerSuccess 服务端Success帧
	TypeServerSuccess FrameType = 0x04
	// TypeError 错误帧
	TypeError FrameType = 0xFF
)

// FrameFlag 帧标志位
type FrameFlag uint8

const (
	// FlagCompressed 压缩标志
	FlagCompressed FrameFlag = 0x01
	// FlagEncrypted 加密标志
	FlagEncrypted FrameFlag = 0x02
)

// Frame Zap认证帧
type Frame struct {
	// 协议魔数
	Magic uint32
	// 协议版本
	Version uint8
	// 帧类型
	Type FrameType
	// 标志位
	Flags FrameFlag
	// 保留字段
	Reserved uint8
	// 载荷长度
	Length uint32
	// 载荷数据
	Payload []byte
}

// NewFrame 创建新帧
func NewFrame(frameType FrameType, payload []byte) *Frame {
	return &Frame{
		Magic:    ProtocolMagic,
		Version:  ProtocolVersion,
		Type:     frameType,
		Flags:    0,
		Reserved: 0,
		Length:   uint32(len(payload)),
		Payload:  payload,
	}
}

// SetFlag 设置标志位
func (f *Frame) SetFlag(flag FrameFlag) {
	f.Flags |= flag
}

// ClearFlag 清除标志位
func (f *Frame) ClearFlag(flag FrameFlag) {
	f.Flags &^= flag
}

// HasFlag 检查是否包含标志位
func (f *Frame) HasFlag(flag FrameFlag) bool {
	return f.Flags&flag != 0
}

// Encode 编码帧
func (f *Frame) Encode() []byte {
	buf := make([]byte, FrameHeaderSize+len(f.Payload))

	// 写入头部
	binary.LittleEndian.PutUint32(buf[0:4], f.Magic)
	buf[4] = f.Version
	buf[5] = uint8(f.Type)
	buf[6] = uint8(f.Flags)
	buf[7] = f.Reserved
	binary.LittleEndian.PutUint32(buf[8:12], f.Length)

	// 写入载荷
	if len(f.Payload) > 0 {
		copy(buf[12:], f.Payload)
	}

	return buf
}

// Decode 解码帧
func Decode(data []byte) (*Frame, error) {
	if len(data) < FrameHeaderSize {
		return nil, errors.New("帧数据过短")
	}

	// 解析头部
	magic := binary.LittleEndian.Uint32(data[0:4])
	if magic != ProtocolMagic {
		return nil, fmt.Errorf("魔数不匹配: 期望 0x%08X, 得到 0x%08X", ProtocolMagic, magic)
	}

	version := data[4]
	if version != ProtocolVersion {
		return nil, fmt.Errorf("协议版本不匹配: 期望 %d, 得到 %d", ProtocolVersion, version)
	}

	length := binary.LittleEndian.Uint32(data[8:12])
	if uint32(len(data)-FrameHeaderSize) < length {
		return nil, fmt.Errorf("载荷长度不匹配: 期望 %d, 实际 %d", length, len(data)-FrameHeaderSize)
	}

	// 提取载荷
	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		copy(payload, data[12:12+length])
	}

	return &Frame{
		Magic:    magic,
		Version:  version,
		Type:     FrameType(data[5]),
		Flags:    FrameFlag(data[6]),
		Reserved: data[7],
		Length:   length,
		Payload:  payload,
	}, nil
}

// ReadFrame 从读取器读取帧
func ReadFrame(r io.Reader) (*Frame, error) {
	// 读取头部
	header := make([]byte, FrameHeaderSize)
	n, err := io.ReadFull(r, header)
	if err != nil {
		if err == io.EOF && n == 0 {
			return nil, io.EOF
		}
		return nil, fmt.Errorf("读取帧头失败: %w", err)
	}

	// 解析头部
	magic := binary.LittleEndian.Uint32(header[0:4])
	if magic != ProtocolMagic {
		return nil, fmt.Errorf("魔数不匹配: 期望 0x%08X, 得到 0x%08X", ProtocolMagic, magic)
	}

	version := header[4]
	if version != ProtocolVersion {
		return nil, fmt.Errorf("协议版本不匹配: 期望 %d, 得到 %d", ProtocolVersion, version)
	}

	length := binary.LittleEndian.Uint32(header[8:12])

	// 读取载荷
	var payload []byte
	if length > 0 {
		payload = make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, fmt.Errorf("读取载荷失败: %w", err)
		}
	}

	return &Frame{
		Magic:    magic,
		Version:  version,
		Type:     FrameType(header[5]),
		Flags:    FrameFlag(header[6]),
		Reserved: header[7],
		Length:   length,
		Payload:  payload,
	}, nil
}

// WriteFrame 写入帧到写入器
func WriteFrame(w io.Writer, frame *Frame) error {
	data := frame.Encode()
	_, err := w.Write(data)
	return err
}

// ClientHelloPayload ClientHello载荷结构
type ClientHelloPayload struct {
	AuthMethod      AuthMethod
	ClientRandom    [32]byte
	ClientPublicKey []byte // 可变长度，ECDH公钥
}

// EncodeClientHello 编码ClientHello载荷
func EncodeClientHello(authMethod AuthMethod, clientRandom []byte, clientPublicKey []byte) []byte {
	// 基本结构: AuthMethod(1) + ClientRandom(32) + PubKeyLen(2) + PubKey
	pubKeyLen := len(clientPublicKey)
	if pubKeyLen > 65535 {
		pubKeyLen = 65535 // 限制长度
	}

	buf := make([]byte, 1+32+2+pubKeyLen)
	buf[0] = uint8(authMethod)
	copy(buf[1:33], clientRandom[:])
	binary.LittleEndian.PutUint16(buf[33:35], uint16(pubKeyLen))
	copy(buf[35:], clientPublicKey[:pubKeyLen])

	return buf
}

// DecodeClientHello 解码ClientHello载荷
func DecodeClientHello(data []byte) (AuthMethod, []byte, []byte, error) {
	if len(data) < 35 {
		return 0, nil, nil, errors.New("ClientHello数据过短")
	}

	authMethod := AuthMethod(data[0])
	clientRandom := make([]byte, 32)
	copy(clientRandom, data[1:33])

	pubKeyLen := binary.LittleEndian.Uint16(data[33:35])
	if len(data) < 35+int(pubKeyLen) {
		return 0, nil, nil, errors.New("公钥数据长度不匹配")
	}

	clientPublicKey := make([]byte, pubKeyLen)
	copy(clientPublicKey, data[35:35+pubKeyLen])

	return authMethod, clientRandom, clientPublicKey, nil
}

// ServerChallengePayload ServerChallenge载荷结构
type ServerChallengePayload struct {
	ServerRandom    [32]byte
	ServerPublicKey []byte // 可变长度，ECDH公钥
	Salt            [16]byte
	Signature       []byte // 可变长度，签名
}

// EncodeServerChallenge 编码ServerChallenge载荷
func EncodeServerChallenge(serverRandom []byte, serverPublicKey []byte, salt []byte, signature []byte) []byte {
	// 结构: ServerRandom(32) + PubKeyLen(2) + PubKey + Salt(16) + SigLen(2) + Signature
	pubKeyLen := len(serverPublicKey)
	sigLen := len(signature)
	if pubKeyLen > 65535 {
		pubKeyLen = 65535
	}
	if sigLen > 65535 {
		sigLen = 65535
	}

	buf := make([]byte, 32+2+pubKeyLen+16+2+sigLen)
	copy(buf[0:32], serverRandom[:])
	binary.LittleEndian.PutUint16(buf[32:34], uint16(pubKeyLen))
	copy(buf[34:34+pubKeyLen], serverPublicKey[:pubKeyLen])
	copy(buf[34+pubKeyLen:50+pubKeyLen], salt[:])
	binary.LittleEndian.PutUint16(buf[50+pubKeyLen:52+pubKeyLen], uint16(sigLen))
	copy(buf[52+pubKeyLen:], signature[:sigLen])

	return buf
}

// DecodeServerChallenge 解码ServerChallenge载荷
func DecodeServerChallenge(data []byte) ([]byte, []byte, []byte, []byte, error) {
	if len(data) < 50 {
		return nil, nil, nil, nil, errors.New("ServerChallenge数据过短")
	}

	serverRandom := make([]byte, 32)
	copy(serverRandom, data[0:32])

	pubKeyLen := binary.LittleEndian.Uint16(data[32:34])
	if len(data) < 34+int(pubKeyLen)+16+2 {
		return nil, nil, nil, nil, errors.New("公钥数据长度不匹配")
	}

	serverPublicKey := make([]byte, pubKeyLen)
	copy(serverPublicKey, data[34:34+pubKeyLen])

	offset := 34 + int(pubKeyLen)
	salt := make([]byte, 16)
	copy(salt, data[offset:offset+16])

	sigLen := binary.LittleEndian.Uint16(data[offset+16 : offset+18])
	if len(data) < offset+18+int(sigLen) {
		return nil, nil, nil, nil, errors.New("签名数据长度不匹配")
	}

	signature := make([]byte, sigLen)
	copy(signature, data[offset+18:offset+18+int(sigLen)])

	return serverRandom, serverPublicKey, salt, signature, nil
}

// ClientResponsePayload ClientResponse载荷结构
type ClientResponsePayload struct {
	AuthData []byte // 可变长度，认证数据
	HMAC     [32]byte
}

// EncodeClientResponse 编码ClientResponse载荷
func EncodeClientResponse(authData []byte, hmac []byte) []byte {
	// 结构: AuthDataLen(2) + AuthData + HMAC(32)
	authDataLen := len(authData)
	if authDataLen > 65535 {
		authDataLen = 65535
	}

	buf := make([]byte, 2+authDataLen+32)
	binary.LittleEndian.PutUint16(buf[0:2], uint16(authDataLen))
	copy(buf[2:2+authDataLen], authData[:authDataLen])
	copy(buf[2+authDataLen:], hmac[:32])

	return buf
}

// DecodeClientResponse 解码ClientResponse载荷
func DecodeClientResponse(data []byte) ([]byte, []byte, error) {
	if len(data) < 34 {
		return nil, nil, errors.New("ClientResponse数据过短")
	}

	authDataLen := binary.LittleEndian.Uint16(data[0:2])
	if len(data) < 2+int(authDataLen)+32 {
		return nil, nil, errors.New("认证数据长度不匹配")
	}

	authData := make([]byte, authDataLen)
	copy(authData, data[2:2+authDataLen])

	hmac := make([]byte, 32)
	copy(hmac, data[2+authDataLen:2+authDataLen+32])

	return authData, hmac, nil
}

// ServerSuccessPayload ServerSuccess载荷结构
type ServerSuccessPayload struct {
	SessionID [16]byte
	TTL       uint32 // 秒
}

// EncodeServerSuccess 编码ServerSuccess载荷
func EncodeServerSuccess(sessionID []byte, ttl uint32) []byte {
	// 结构: SessionID(16) + TTL(4)
	buf := make([]byte, 20)
	copy(buf[0:16], sessionID[:16])
	binary.LittleEndian.PutUint32(buf[16:20], ttl)
	return buf
}

// DecodeServerSuccess 解码ServerSuccess载荷
func DecodeServerSuccess(data []byte) ([]byte, uint32, error) {
	if len(data) < 20 {
		return nil, 0, errors.New("ServerSuccess数据过短")
	}

	sessionID := make([]byte, 16)
	copy(sessionID, data[0:16])
	ttl := binary.LittleEndian.Uint32(data[16:20])

	return sessionID, ttl, nil
}

// ErrorPayload 错误载荷
type ErrorPayload struct {
	Code    uint16
	Message string
}

// EncodeError 编码错误载荷
func EncodeError(code uint16, message string) []byte {
	msgBytes := []byte(message)
	msgLen := len(msgBytes)
	if msgLen > 65535 {
		msgLen = 65535
		msgBytes = msgBytes[:msgLen]
	}

	buf := make([]byte, 2+2+msgLen)
	binary.LittleEndian.PutUint16(buf[0:2], code)
	binary.LittleEndian.PutUint16(buf[2:4], uint16(msgLen))
	copy(buf[4:], msgBytes)

	return buf
}

// DecodeError 解码错误载荷
func DecodeError(data []byte) (uint16, string, error) {
	if len(data) < 4 {
		return 0, "", errors.New("错误数据过短")
	}

	code := binary.LittleEndian.Uint16(data[0:2])
	msgLen := binary.LittleEndian.Uint16(data[2:4])

	if len(data) < 4+int(msgLen) {
		return 0, "", errors.New("错误消息长度不匹配")
	}

	message := string(data[4 : 4+msgLen])
	return code, message, nil
}
