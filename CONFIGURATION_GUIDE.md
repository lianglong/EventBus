# EventBus 配置指南

## AsyncWorkers 配置建议

`AsyncWorkers` 是最重要的配置参数，它决定了事件处理的并发模式和性能特征。

---

## 🎯 快速选择

| 场景 | 推荐值 | 说明 |
|------|--------|------|
| 简单同步场景 | `0` | 发布时阻塞等待，适合低频事件 |
| 发送邮件/通知 | `10-20` | I/O 密集，适度并发 |
| 数据库写入 | `20-50` | I/O 密集，需要较高并发 |
| HTTP 请求 | `50-100` | 网络 I/O，高并发 |
| CPU 计算 | `runtime.NumCPU()` | CPU 密集，避免过度并发 |
| 混合场景 | `runtime.NumCPU() * 2` | CPU + I/O 混合 |
| 高吞吐量 | `100-200` | 最大化吞吐，但注意内存 |

---

## 📊 详细分析

### 1. 同步模式 (AsyncWorkers = 0)

```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 0,  // 同步模式
})
```

**特点：**
- ✅ 简单直观，易于调试
- ✅ 保证执行顺序
- ✅ 错误立即可见
- ❌ 阻塞发布者
- ❌ 吞吐量有限

**适用场景：**
- 事件频率低（< 100/秒）
- 需要保证执行顺序
- 需要立即获取执行结果
- 测试和开发环境

**示例：**
```go
// 用户登录事件，需要同步完成鉴权、记录日志
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 0,
    PublishTimeout: 5 * time.Second,  // 限制总执行时间
})

dispatcher.SubscribeFunc("user.login", func(ctx context.Context, e EventBus.Event) error {
    // 鉴权
    if err := authenticate(ctx, e); err != nil {
        return err
    }
    // 记录日志
    return logLogin(ctx, e)
}, EventBus.NormalPriority)

// 发布时会阻塞直到所有 handler 执行完
err := dispatcher.Publish(UserLoginEvent{UserID: "123"})
if err != nil {
    // 可以立即处理错误
    log.Printf("登录处理失败: %v", err)
}
```

---

### 2. CPU 密集型 (AsyncWorkers = NumCPU)

```go
import "runtime"

dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: runtime.NumCPU(),  // 4 核 = 4 workers
})
```

**特点：**
- ✅ 充分利用 CPU
- ✅ 避免过度竞争
- ✅ 低上下文切换开销
- ⚠️ 不适合 I/O 密集

**适用场景：**
- 图像处理
- 数据分析
- 加密/解密
- 压缩/解压

**示例：**
```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: runtime.NumCPU(),
})

dispatcher.SubscribeFunc("image.process", func(ctx context.Context, e EventBus.Event) error {
    event := e.(ImageProcessEvent)

    // CPU 密集：图像缩放、滤镜等
    img := loadImage(event.ImagePath)
    resized := resizeImage(img, event.Width, event.Height)

    return saveImage(resized, event.OutputPath)
}, EventBus.NormalPriority)

// 批量处理 1000 张图片
// NumCPU 个 worker 会并发处理，不会创建过多线程
for i := 0; i < 1000; i++ {
    dispatcher.Publish(ImageProcessEvent{...})
}
```

---

### 3. I/O 密集型 (AsyncWorkers = NumCPU * 2 ~ 10)

```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: runtime.NumCPU() * 5,  // 4 核 = 20 workers
})
```

**特点：**
- ✅ 在 I/O 等待时可以处理其他任务
- ✅ 提高吞吐量
- ⚠️ 不要设置太大（> NumCPU * 10）

**适用场景：**
- 数据库操作
- 文件读写
- 网络请求
- 消息队列

**示例：**
```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: runtime.NumCPU() * 5,  // 20 workers
})

dispatcher.SubscribeFunc("order.created", func(ctx context.Context, e EventBus.Event) error {
    event := e.(OrderEvent)

    // I/O 密集：数据库写入
    if err := db.SaveOrder(ctx, event.Order); err != nil {
        return err
    }

    // I/O 密集：发送 HTTP 通知
    if err := notifyService(ctx, event.Order); err != nil {
        return err
    }

    return nil
}, EventBus.NormalPriority)

// 高并发订单，20 个 worker 可以同时处理
// 当某些 worker 在等待数据库响应时，其他 worker 继续工作
```

---

### 4. 低延迟场景 (AsyncWorkers = 5 ~ 20)

```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 10,  // 小规模 worker 池
})
```

**特点：**
- ✅ 快速响应
- ✅ 低内存开销
- ✅ 适合实时性要求高的场景
- ⚠️ 吞吐量可能不如大 worker 池

**适用场景：**
- 实时通知
- 监控告警
- 日志收集
- 消息推送

**示例：**
```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 10,
    PublishTimeout: 100 * time.Millisecond,  // 快速失败
})

dispatcher.SubscribeFunc("alert.critical", func(ctx context.Context, e EventBus.Event) error {
    event := e.(AlertEvent)

    // 要求低延迟：立即发送告警
    return sendAlert(ctx, event)
}, EventBus.HighPriority)

// 关键告警需要快速处理
// 小 worker 池 + 短超时确保低延迟
err := dispatcher.PublishContext(ctx, CriticalAlert{...})
if err == EventBus.ErrPublishTimeout {
    // 如果 100ms 内无法入队，说明系统过载
    log.Error("告警系统过载")
}
```

---

### 5. 高吞吐量场景 (AsyncWorkers = 50 ~ 200)

```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 100,  // 大规模 worker 池
})
```

**特点：**
- ✅ 最大化吞吐量
- ✅ 适合海量事件
- ❌ 高内存开销
- ❌ 高 GC 压力
- ❌ 上下文切换成本高

**适用场景：**
- 日志系统
- 埋点上报
- 消息队列消费
- 批量数据处理

**示例：**
```go
dispatcher := EventBus.New(&EventBus.Options{
    AsyncWorkers: 100,  // 100 个 worker
})

dispatcher.SubscribeFunc("log.write", func(ctx context.Context, e EventBus.Event) error {
    event := e.(LogEvent)

    // 批量写入日志文件
    return writeLog(event)
}, EventBus.NormalPriority)

// 每秒数万条日志
// 大 worker 池可以快速消费
for _, logEntry := range millionsOfLogs {
    dispatcher.Publish(LogEvent{Entry: logEntry})
}
```

---

## 📐 计算公式

### 基础公式
```
AsyncWorkers = NumCPU × 并发系数
```

### 并发系数建议

| 任务类型 | 并发系数 | 说明 |
|---------|---------|------|
| 纯 CPU 计算 | 1.0 | 避免线程竞争 |
| 轻量 I/O | 2.0 | 少量网络/文件操作 |
| 中等 I/O | 3-5 | 频繁数据库/网络请求 |
| 重 I/O | 5-10 | 大量外部调用 |
| 极端高并发 | 10-20 | 需要压测验证 |

### 实际案例

**4 核 CPU 服务器：**
```go
NumCPU := 4

// CPU 密集：图像处理
AsyncWorkers = 4 × 1.0 = 4

// 数据库操作
AsyncWorkers = 4 × 5 = 20

// HTTP 请求
AsyncWorkers = 4 × 8 = 32

// 高吞吐日志
AsyncWorkers = 4 × 25 = 100
```

---

## ⚠️ 注意事项

### 1. 队列容量
```go
队列容量 = AsyncWorkers × 10
```

如果 `AsyncWorkers = 20`，则队列可以缓存 200 个待处理任务。

**影响：**
- 队列太小：发布时容易阻塞（等待入队）
- 队列太大：内存占用高，任务积压

### 2. 内存开销

每个 worker 是一个 goroutine，约占用 2-8 KB 内存（初始栈）。

```
内存开销 ≈ AsyncWorkers × 4 KB （平均值）
```

**示例：**
- 10 workers ≈ 40 KB
- 100 workers ≈ 400 KB
- 1000 workers ≈ 4 MB

### 3. 上下文切换

当 worker 数量超过 CPU 核心数很多时，会增加上下文切换开销。

**建议：**
- `AsyncWorkers` 不要超过 `NumCPU × 20`（除非特殊场景）
- 通过压测验证性能

### 4. GC 压力

大量 goroutine 会增加 GC 扫描成本。

**建议：**
- 如果 `AsyncWorkers > 100`，关注 GC 指标
- 使用 `runtime.SetGCPercent` 调优

---

## 🧪 性能调优步骤

### 1. 基准测试

```go
func BenchmarkPublish(b *testing.B) {
    workers := []int{0, 5, 10, 20, 50, 100}

    for _, w := range workers {
        b.Run(fmt.Sprintf("workers=%d", w), func(b *testing.B) {
            dispatcher := EventBus.New(&EventBus.Options{
                AsyncWorkers: w,
            })
            defer dispatcher.Close()

            dispatcher.SubscribeFunc("test", func(ctx context.Context, e EventBus.Event) error {
                // 模拟实际工作负载
                time.Sleep(10 * time.Millisecond)
                return nil
            }, EventBus.NormalPriority)

            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                dispatcher.Publish(TestEvent{})
            }
        })
    }
}
```

运行：
```bash
go test -bench=BenchmarkPublish -benchmem
```

### 2. 压力测试

```go
func TestLoadTest(t *testing.T) {
    dispatcher := EventBus.New(&EventBus.Options{
        AsyncWorkers: 20,
    })
    defer dispatcher.Close()

    var processed int64
    dispatcher.SubscribeFunc("load", func(ctx context.Context, e EventBus.Event) error {
        atomic.AddInt64(&processed, 1)
        // 模拟真实负载
        time.Sleep(50 * time.Millisecond)
        return nil
    }, EventBus.NormalPriority)

    // 发送 10000 个事件
    start := time.Now()
    for i := 0; i < 10000; i++ {
        dispatcher.Publish(LoadEvent{ID: i})
    }

    // 等待处理完成
    dispatcher.Close()

    duration := time.Since(start)
    qps := float64(processed) / duration.Seconds()

    fmt.Printf("处理 %d 个事件，耗时 %v，QPS: %.2f\n", processed, duration, qps)
}
```

### 3. 监控指标

建议监控以下指标：

```go
type Metrics struct {
    // 队列深度
    QueueDepth int
    // 活跃 worker 数
    ActiveWorkers int
    // 事件处理速率
    EventsPerSecond float64
    // 平均延迟
    AvgLatency time.Duration
    // 错误率
    ErrorRate float64
}
```

---

## 📋 配置检查清单

在生产环境部署前，检查以下内容：

- [ ] 根据任务类型选择了合适的 `AsyncWorkers`
- [ ] 通过基准测试验证了性能
- [ ] 设置了合理的 `PublishTimeout`
- [ ] 配置了 `ErrorHandler` 用于监控
- [ ] 测试了队列满的情况
- [ ] 验证了内存和 CPU 使用率
- [ ] 设置了资源限制（如果在容器中）

---

## 🎓 最佳实践示例

### 电商系统配置

```go
package main

import (
    "runtime"
    "github.com/lianglong/EventBus/v2"
)

func main() {
    // 订单处理：I/O 密集（数据库 + 第三方服务）
    orderDispatcher := EventBus.New(&EventBus.Options{
        AsyncWorkers: runtime.NumCPU() * 5,  // 20 workers (假设 4 核)
        PublishTimeout: 100 * time.Millisecond,
        ErrorHandler: func(e EventBus.Event, err error) {
            // 发送告警
            alerting.SendAlert("订单处理失败", err)
        },
    })

    // 日志收集：高吞吐量
    logDispatcher := EventBus.New(&EventBus.Options{
        AsyncWorkers: 100,
        PublishTimeout: 10 * time.Millisecond,  // 快速失败
        ErrorHandler: func(e EventBus.Event, err error) {
            // 降级：写入本地文件
            writeToLocalFile(e)
        },
    })

    // 实时通知：低延迟
    notifyDispatcher := EventBus.New(&EventBus.Options{
        AsyncWorkers: 10,
        PublishTimeout: 50 * time.Millisecond,
        ErrorHandler: func(e EventBus.Event, err error) {
            // 重试队列
            retryQueue.Push(e)
        },
    })
}
```

---

## 🔗 相关文档

- [使用示例](./EXAMPLES.md)
- [性能优化指南](./PERFORMANCE.md)
- [故障排查](./TROUBLESHOOTING.md)

---

## 💡 总结

**核心原则：**
1. 从小开始，逐步增加
2. 根据实际负载调整
3. 持续监控和优化
4. 不要过度优化

**推荐起始值：**
- 开发/测试：`0` 或 `5`
- 生产环境：`runtime.NumCPU() * 2`
- 根据监控数据逐步调整

**记住：**
> 没有银弹，最佳值取决于你的具体场景。通过基准测试和监控找到最适合的配置。
