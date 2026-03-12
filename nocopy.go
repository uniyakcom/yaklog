package yaklog

import "sync"

// noCopy 禁止拷贝哨兵。
//
// 嵌入到不应被拷贝的结构体中（如包含互斥锁或 goroutine 的类型），
// go vet 的 copylocks 分析器会在赋值/函数传参时发出警告。
//
// 使用示例：
//
//	type asyncWriter struct {
//	    noCopy noCopy
//	    // ...
//	}
type noCopy struct{}

// Lock 满足 sync.Locker 接口，供 go vet copylocks 识别。
func (*noCopy) Lock() {}

// Unlock 满足 sync.Locker 接口。
func (*noCopy) Unlock() {}

// 编译期验证：noCopy 实现 sync.Locker 接口。
var _ sync.Locker = (*noCopy)(nil)
