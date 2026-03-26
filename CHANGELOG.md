# Changelog

## [v3.0.0]

### Breaking Changes

#### Middleware 签名变更

中间件签名改为标准洋葱模型，去掉了 `context.Context` 和 `Event` 参数：

```go
// v2
type Middleware func(context.Context, Event, HandleFunc) HandleFunc

// v3
type Middleware func(HandleFunc) HandleFunc
```

迁移示例：

```go
// v2
func LogMiddleware(ctx context.Context, e EventBus.Event, next EventBus.HandleFunc) EventBus.HandleFunc {
    return func(ctx context.Context, event EventBus.Event) error {
        log.Printf("before: %s", event.EventName())
        err := next(ctx, event)
        log.Printf("after: %s", event.EventName())
        return err
    }
}

// v3
func LogMiddleware(next EventBus.HandleFunc) EventBus.HandleFunc {
    return func(ctx context.Context, event EventBus.Event) error {
        log.Printf("before: %s", event.EventName())
        err := next(ctx, event)
        log.Printf("after: %s", event.EventName())
        return err
    }
}
```

#### Subscribe 返回值变更

`Subscribe` 现在同时返回订阅句柄和错误：

```go
// v2
func (d *Dispatcher) Subscribe(listener Listener) []*Subscription

// v3
func (d *Dispatcher) Subscribe(listener Listener) ([]*Subscription, error)
```

迁移示例：

```go
// v2
subs := dispatcher.Subscribe(MyListener{})

// v3
subs, err := dispatcher.Subscribe(MyListener{})
if err != nil {
    // dispatcher 已关闭
}
```

### Bug Fixes

- **修复并发写竞态**：`Subscribe` / `Unsubscribe` 并发调用时存在 map 写覆盖风险，现通过 `sync.Mutex` 保护写路径，读路径仍无锁
- **修复 Close() 顺序错误**：原实现 `close(taskCh)` 与 `wg.Wait()` 顺序颠倒，导致 worker 无法正常退出，现已修正

### Improvements

- **Worker 退出机制优化**：worker 改用 `for range taskCh` 消费任务，`Close()` 通过 `close(taskCh)` 通知退出，worker 排空队列后自然退出，移除了冗余的 `closeCh` 字段
- **同步发布去除多余 goroutine**：同步模式下 `PublishContext` 直接调用 `executeListeners`，不再创建额外的 goroutine + channel
- **中间件执行顺序明确化**：`[A, B, C]` 的执行顺序为 `A → B → C → handler → C → B → A`，与 gin、grpc 等主流框架一致
- **提取 `buildChain` 方法**：中间件链构建逻辑独立为 `buildChain`，逻辑更清晰

---

## [v2.0.0]

初始 v2 发布。
