package zop

import (
	"bytes"
	"context"
	"testing"
)

func TestStreamHTTP3Obfuscator(t *testing.T) {
	config := &Config{
		EnabledModes: []Mode{ModeHTTP3},
		ModeConfigs: map[Mode]ModeConfig{
			ModeHTTP3: {
				HTTP3: HTTP3Config{
					Method:       "GET",
					HostHeader:   "example.com",
					UserAgent:    "TestAgent",
					PathTemplate: "/api/v{version}/data/{id}",
				},
			},
		},
	}

	// 创建混淆器
	obfuscator, err := newHTTP3Obfuscator(config)
	if err != nil {
		t.Fatalf("创建HTTP/3混淆器失败: %v", err)
	}

	// 检查是否实现了StreamObfuscator接口
	streamObf, ok := obfuscator.(StreamObfuscator)
	if !ok {
		t.Fatal("HTTP/3混淆器未实现StreamObfuscator接口")
	}

	// 测试数据
	testData := []byte("Hello, World! This is a test data for stream obfuscation.")

	// 测试流式混淆
	t.Run("StreamObfuscate", func(t *testing.T) {
		src := bytes.NewReader(testData)
		var dstBuf bytes.Buffer

		n, err := streamObf.StreamObfuscate(context.Background(), src, &dstBuf)
		if err != nil {
			t.Fatalf("StreamObfuscate失败: %v", err)
		}

		if n != int64(len(testData)) {
			t.Errorf("写入字节数不匹配: 期望 %d, 实际 %d", len(testData), n)
		}

		// 验证混淆后的数据可以解混淆
		obfuscatedData := dstBuf.Bytes()
		if len(obfuscatedData) == 0 {
			t.Fatal("混淆后数据为空")
		}

		// 使用传统Deobfuscate验证
		deobfuscated, err := obfuscator.Deobfuscate(context.Background(), obfuscatedData)
		if err != nil {
			t.Fatalf("传统Deobfuscate失败: %v", err)
		}

		if !bytes.Equal(deobfuscated, testData) {
			t.Errorf("解混淆数据不匹配: 期望 %v, 实际 %v", testData, deobfuscated)
		}
	})

	// 测试流式解混淆
	t.Run("StreamDeobfuscate", func(t *testing.T) {
		// 先使用传统方法混淆数据
		obfuscated, err := obfuscator.Obfuscate(context.Background(), testData)
		if err != nil {
			t.Fatalf("传统Obfuscate失败: %v", err)
		}

		src := bytes.NewReader(obfuscated)
		var dstBuf bytes.Buffer

		n, err := streamObf.StreamDeobfuscate(context.Background(), src, &dstBuf)
		if err != nil {
			t.Fatalf("StreamDeobfuscate失败: %v", err)
		}

		if n != int64(len(testData)) {
			t.Errorf("解混淆字节数不匹配: 期望 %d, 实际 %d", len(testData), n)
		}

		deobfuscatedData := dstBuf.Bytes()
		if !bytes.Equal(deobfuscatedData, testData) {
			t.Errorf("流式解混淆数据不匹配: 期望 %v, 实际 %v", testData, deobfuscatedData)
		}
	})

	// 测试大流量数据
	t.Run("LargeDataStream", func(t *testing.T) {
		// 生成1MB测试数据
		largeData := make([]byte, 1024*1024)
		for i := range largeData {
			largeData[i] = byte(i % 256)
		}

		src := bytes.NewReader(largeData)
		var obfuscatedBuf bytes.Buffer

		// 流式混淆
		n, err := streamObf.StreamObfuscate(context.Background(), src, &obfuscatedBuf)
		if err != nil {
			t.Fatalf("大流量StreamObfuscate失败: %v", err)
		}

		if n != int64(len(largeData)) {
			t.Errorf("大流量写入字节数不匹配: 期望 %d, 实际 %d", len(largeData), n)
		}

		// 流式解混淆
		obfuscatedReader := bytes.NewReader(obfuscatedBuf.Bytes())
		var deobfuscatedBuf bytes.Buffer

		n2, err := streamObf.StreamDeobfuscate(context.Background(), obfuscatedReader, &deobfuscatedBuf)
		if err != nil {
			t.Fatalf("大流量StreamDeobfuscate失败: %v", err)
		}

		if n2 != int64(len(largeData)) {
			t.Errorf("大流量解混淆字节数不匹配: 期望 %d, 实际 %d", len(largeData), n2)
		}

		deobfuscatedData := deobfuscatedBuf.Bytes()
		if !bytes.Equal(deobfuscatedData, largeData) {
			t.Error("大流量数据解混淆后不匹配")
		}
	})

	// 测试零字节数据
	t.Run("ZeroByteStream", func(t *testing.T) {
		emptyData := []byte{}
		src := bytes.NewReader(emptyData)
		var dstBuf bytes.Buffer

		n, err := streamObf.StreamObfuscate(context.Background(), src, &dstBuf)
		if err != nil {
			t.Fatalf("零字节StreamObfuscate失败: %v", err)
		}

		if n != 0 {
			t.Errorf("零字节写入字节数不匹配: 期望 0, 实际 %d", n)
		}

		obfuscatedData := dstBuf.Bytes()
		if len(obfuscatedData) == 0 {
			// 零字节数据可能产生空输出或只包含HEADERS帧
			t.Log("零字节数据混淆后为空（可能只包含HEADERS帧）")
		}
	})
}
