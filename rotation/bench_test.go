package rotation_test

import (
	"bytes"
	"testing"

	"github.com/uniyakcom/yaklog/rotation"
)

// BenchmarkRotatingWriter_Write 测量单次 Write 的吞吐（未触发轮转）。
func BenchmarkRotatingWriter_Write(b *testing.B) {
	dir := b.TempDir()
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("bench"),
		rotation.WithMaxSize(1<<30), // 1 GiB，不触发轮转
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = w.Close() })

	payload := bytes.Repeat([]byte("x"), 256)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := w.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkRotatingWriter_WriteWithRotation 测量含频繁轮转时的写入吞吐。
func BenchmarkRotatingWriter_WriteWithRotation(b *testing.B) {
	dir := b.TempDir()
	w, err := rotation.New(
		rotation.WithDir(dir),
		rotation.WithFilename("bench"),
		rotation.WithMaxSize(1<<10), // 1 KiB，频繁轮转
		rotation.WithMaxBackups(5),
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = w.Close() })

	payload := bytes.Repeat([]byte("y"), 128)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := w.Write(payload); err != nil {
			b.Fatal(err)
		}
	}
}
