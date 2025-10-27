# EventBus 使用示例

本文档展示了如何使用 EventBus 的各种功能，特别是 context 相关的特性。

## 目录
- [基础使用](#基础使用)
- [使用 Context 传递值](#使用-context-传递值)
- [使用 Context 控制超时](#使用-context-控制超时)
- [使用 Context 取消操作](#使用-context-取消操作)
- [中间件使用](#中间件使用)
- [异步发布](#异步发布)
- [错误处理](#错误处理)

---

## 基础使用
```go
go get -u -v github.com/lianglong/EventBus
```
```go
package main

import (
	"context"
	"fmt"
	"github.com/lianglong/EventBus"
)

// 定义事件
type UserLoginEvent struct {
	UserID string
	IP     string
}

func (UserLoginEvent) EventName() string {
	return "user.login"
}

// 定义监听器
type NotificationListener struct{}

func (NotificationListener) ListenEvents() []EventBus.Event {
	return []EventBus.Event{UserLoginEvent{}}
}

func (NotificationListener) Priority() int32 {
	return EventBus.NormalPriority
}

func (NotificationListener) Handle(ctx context.Context, e EventBus.Event) error {
	event := e.(UserLoginEvent)
	fmt.Printf("发送登录通知给用户: %s\n", event.UserID)
	return nil
}

func main() {
	// 创建 dispatcher
	dispatcher := EventBus.New()
	defer dispatcher.Close()

	// 订阅事件
	dispatcher.Subscribe(NotificationListener{})

	// 发布事件
	dispatcher.Publish(UserLoginEvent{
		UserID: "user-123",
		IP:     "192.168.1.1",
	})
}
```

---

## 使用 Context 传递值

Context 可以用来在发布者和监听器之间传递请求级别的数据。

```go
package main

import (
	"context"
	"fmt"
	"github.com/lianglong/EventBus"
)

type OrderCreatedEvent struct {
	OrderID string
	Amount  float64
}

func (OrderCreatedEvent) EventName() string {
	return "order.created"
}

func main() {
	dispatcher := EventBus.New()
	defer dispatcher.Close()

	// 订阅事件，从 context 获取值
	dispatcher.SubscribeFunc("order.created", func(ctx context.Context, e EventBus.Event) error {
		event := e.(OrderCreatedEvent)

		// 从 context 获取用户信息
		userID := ctx.Value("userID").(string)
		requestID := ctx.Value("requestID").(string)

		fmt.Printf("订单 %s 创建成功\n", event.OrderID)
		fmt.Printf("用户: %s, 请求ID: %s\n", userID, requestID)

		return nil
	}, EventBus.NormalPriority)

	// 发布时传递 context 值
	ctx := context.Background()
	ctx = context.WithValue(ctx, "userID", "user-456")
	ctx = context.WithValue(ctx, "requestID", "req-789")

	dispatcher.PublishContext(ctx, OrderCreatedEvent{
		OrderID: "order-001",
		Amount:  99.99,
	})
}
```

---

## 使用 Context 控制超时

```go
package main

import (
	"context"
	"fmt"
	"time"
	"github.com/lianglong/EventBus"
)

type HeavyTaskEvent struct {
	TaskID string
}

func (HeavyTaskEvent) EventName() string {
	return "heavy.task"
}

func main() {
	dispatcher := EventBus.New()
	defer dispatcher.Close()

	// 订阅耗时任务
	dispatcher.SubscribeFunc("heavy.task", func(ctx context.Context, e EventBus.Event) error {
		event := e.(HeavyTaskEvent)

		// 模拟耗时操作，每步检查 context
		for i := 0; i < 10; i++ {
			select {
			case <-ctx.Done():
				fmt.Printf("任务 %s 被取消: %v\n", event.TaskID, ctx.Err())
				return ctx.Err()
			default:
				// 执行实际工作
				time.Sleep(100 * time.Millisecond)
				fmt.Printf("任务 %s 进度: %d/10\n", event.TaskID, i+1)
			}
		}

		return nil
	}, EventBus.NormalPriority)

	// 设置 500ms 超时（任务需要 1 秒）
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := dispatcher.PublishContext(ctx, HeavyTaskEvent{TaskID: "task-001"})
	if err != nil {
		fmt.Printf("发布失败: %v\n", err)
	}
}
```

---

## 使用 Context 取消操作

```go
package main

import (
	"context"
	"fmt"
	"time"
	"github.com/lianglong/EventBus"
)

type DataProcessEvent struct {
	Data []string
}

func (DataProcessEvent) EventName() string {
	return "data.process"
}

func main() {
	dispatcher := EventBus.New()
	defer dispatcher.Close()

	// 订阅数据处理事件
	dispatcher.SubscribeFunc("data.process", func(ctx context.Context, e EventBus.Event) error {
		event := e.(DataProcessEvent)

		for i, item := range event.Data {
			// 检查是否取消
			select {
			case <-ctx.Done():
				fmt.Printf("处理被取消，已处理 %d/%d 项\n", i, len(event.Data))
				return ctx.Err()
			default:
				fmt.Printf("处理项 %d: %s\n", i+1, item)
				time.Sleep(100 * time.Millisecond)
			}
		}

		return nil
	}, EventBus.NormalPriority)

	// 创建可取消的 context
	ctx, cancel := context.WithCancel(context.Background())

	// 启动一个 goroutine 在 300ms 后取消
	go func() {
		time.Sleep(300 * time.Millisecond)
		fmt.Println("用户请求取消操作...")
		cancel()
	}()

	dispatcher.PublishContext(ctx, DataProcessEvent{
		Data: []string{"item1", "item2", "item3", "item4", "item5"},
	})
}
```

---

## 中间件使用

中间件可以在事件处理前后添加通用逻辑。

```go
package main

import (
	"context"
	"fmt"
	"time"
	"github.com/lianglong/EventBus"
)

// 日志中间件
func LoggingMiddleware(ctx context.Context, e EventBus.Event, next EventBus.HandleFunc) EventBus.HandleFunc {
	return func(ctx context.Context, event EventBus.Event) error {
		start := time.Now()
		fmt.Printf("[LOG] 开始处理事件: %s\n", event.EventName())

		err := next(ctx, event)

		duration := time.Since(start)
		if err != nil {
			fmt.Printf("[LOG] 事件处理失败: %s, 耗时: %v, 错误: %v\n", event.EventName(), duration, err)
		} else {
			fmt.Printf("[LOG] 事件处理成功: %s, 耗时: %v\n", event.EventName(), duration)
		}

		return err
	}
}

// 追踪中间件 - 为每个事件添加追踪 ID
func TracingMiddleware(ctx context.Context, e EventBus.Event, next EventBus.HandleFunc) EventBus.HandleFunc {
	return func(ctx context.Context, event EventBus.Event) error {
		// 如果 context 中没有 traceID，添加一个
		if ctx.Value("traceID") == nil {
			traceID := fmt.Sprintf("trace-%d", time.Now().UnixNano())
			ctx = context.WithValue(ctx, "traceID", traceID)
			fmt.Printf("[TRACE] 创建追踪 ID: %s\n", traceID)
		}

		return next(ctx, event)
	}
}

// 重试中间件
func RetryMiddleware(maxRetries int) EventBus.Middleware {
	return func(ctx context.Context, e EventBus.Event, next EventBus.HandleFunc) EventBus.HandleFunc {
		return func(ctx context.Context, event EventBus.Event) error {
			var err error
			for i := 0; i <= maxRetries; i++ {
				err = next(ctx, event)
				if err == nil {
					return nil
				}

				if i < maxRetries {
					fmt.Printf("[RETRY] 重试 %d/%d: %v\n", i+1, maxRetries, err)
					time.Sleep(time.Duration(i+1) * 100 * time.Millisecond)
				}
			}
			return err
		}
	}
}

func main() {
	dispatcher := EventBus.New(&EventBus.Options{
		Middlewares: []EventBus.Middleware{
			LoggingMiddleware,
			TracingMiddleware,
			RetryMiddleware(3),
		},
	})
	defer dispatcher.Close()

	dispatcher.SubscribeFunc("payment.process", func(ctx context.Context, e EventBus.Event) error {
		if traceID, ok := ctx.Value("traceID").(string); ok {
			fmt.Printf("处理支付，追踪ID: %s\n", traceID)
		}
		return nil
	}, EventBus.NormalPriority)

	dispatcher.Publish(PaymentEvent{Amount: 100.00})
}
```

---

## 异步发布

使用 worker 池异步处理事件，提高性能。

```go
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"
	"github.com/lianglong/EventBus"
)

type EmailEvent struct {
	To      string
	Subject string
	Body    string
}

func (EmailEvent) EventName() string {
	return "email.send"
}

func main() {
	// 创建异步 dispatcher，使用 10 个 worker
	dispatcher := EventBus.New(&EventBus.Options{
		AsyncWorkers: 10,
		PublishTimeout: 5 * time.Second,
		ErrorHandler: func(e EventBus.Event, err error) {
			fmt.Printf("错误: %v, 事件: %v\n", err, e)
		},
	})
	defer dispatcher.Close()

	var processedCount int32

	// 订阅邮件发送事件
	dispatcher.SubscribeFunc("email.send", func(ctx context.Context, e EventBus.Event) error {
		event := e.(EmailEvent)

		// 模拟发送邮件
		time.Sleep(100 * time.Millisecond)
		atomic.AddInt32(&processedCount, 1)

		fmt.Printf("发送邮件给 %s: %s\n", event.To, event.Subject)
		return nil
	}, EventBus.NormalPriority)

	// 批量发布事件（异步处理）
	start := time.Now()
	for i := 0; i < 100; i++ {
		dispatcher.Publish(EmailEvent{
			To:      fmt.Sprintf("user%d@example.com", i),
			Subject: "Welcome",
			Body:    "欢迎使用我们的服务",
		})
	}

	fmt.Printf("发布 100 个事件耗时: %v\n", time.Since(start))

	// 等待所有事件处理完成
	dispatcher.Close()

	fmt.Printf("处理完成，总计: %d\n", atomic.LoadInt32(&processedCount))
}
```

---

## 错误处理

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/lianglong/EventBus"
)

type PaymentEvent struct {
	Amount float64
}

func (PaymentEvent) EventName() string {
	return "payment.process"
}

func main() {
	// 配置错误处理器
	dispatcher := EventBus.New(&EventBus.Options{
		ErrorHandler: func(e EventBus.Event, err error) {
			// 记录错误到日志系统
			fmt.Printf("[ERROR] 事件处理失败\n")
			fmt.Printf("  事件: %s\n", e.EventName())
			fmt.Printf("  错误: %v\n", err)

			// 可以在这里发送告警、记录到数据库等
		},
	})
	defer dispatcher.Close()

	// 订阅可能出错的处理器
	dispatcher.SubscribeFunc("payment.process", func(ctx context.Context, e EventBus.Event) error {
		event := e.(PaymentEvent)

		if event.Amount < 0 {
			return errors.New("金额不能为负数")
		}

		if event.Amount > 10000 {
			return errors.New("金额超过限制")
		}

		fmt.Printf("处理支付: %.2f\n", event.Amount)
		return nil
	}, EventBus.NormalPriority)

	// 发布会触发错误的事件
	dispatcher.Publish(PaymentEvent{Amount: -50.0})   // 触发错误
	dispatcher.Publish(PaymentEvent{Amount: 20000.0}) // 触发错误
	dispatcher.Publish(PaymentEvent{Amount: 100.0})   // 正常
}
```

---

## 完整示例：电商订单系统

```go
package main

import (
	"context"
	"fmt"
	"time"
	"github.com/lianglong/EventBus"
)

// 定义事件
type OrderCreatedEvent struct {
	OrderID string
	UserID  string
	Amount  float64
}

func (OrderCreatedEvent) EventName() string { return "order.created" }

type OrderPaidEvent struct {
	OrderID string
	Amount  float64
}

func (OrderPaidEvent) EventName() string { return "order.paid" }

// 库存监听器
type InventoryListener struct{}

func (InventoryListener) ListenEvents() []EventBus.Event {
	return []EventBus.Event{OrderCreatedEvent{}}
}

func (InventoryListener) Priority() int32 { return EventBus.HighPriority }

func (InventoryListener) Handle(ctx context.Context, e EventBus.Event) error {
	event := e.(OrderCreatedEvent)
	fmt.Printf("[库存] 锁定订单 %s 的库存\n", event.OrderID)
	return nil
}

// 通知监听器
type NotificationListener struct{}

func (NotificationListener) ListenEvents() []EventBus.Event {
	return []EventBus.Event{OrderCreatedEvent{}, OrderPaidEvent{}}
}

func (NotificationListener) Priority() int32 { return EventBus.NormalPriority }

func (NotificationListener) Handle(ctx context.Context, e EventBus.Event) error {
	switch event := e.(type) {
	case OrderCreatedEvent:
		userID := ctx.Value("userID").(string)
		fmt.Printf("[通知] 发送订单创建通知给用户 %s\n", userID)
	case OrderPaidEvent:
		fmt.Printf("[通知] 发送支付成功通知，订单 %s\n", event.OrderID)
	}
	return nil
}

func main() {
	// 创建配置了中间件和异步处理的 dispatcher
	dispatcher := EventBus.New(&EventBus.Options{
		AsyncWorkers: 5,
		Middlewares: []EventBus.Middleware{
			func(ctx context.Context, e EventBus.Event, next EventBus.HandleFunc) EventBus.HandleFunc {
				return func(ctx context.Context, event EventBus.Event) error {
					start := time.Now()
					err := next(ctx, event)
					fmt.Printf("[监控] %s 处理耗时: %v\n", event.EventName(), time.Since(start))
					return err
				}
			},
		},
		ErrorHandler: func(e EventBus.Event, err error) {
			fmt.Printf("[错误] 事件 %s 处理失败: %v\n", e.EventName(), err)
		},
	})
	defer dispatcher.Close()

	// 注册监听器
	dispatcher.Subscribe(InventoryListener{})
	dispatcher.Subscribe(NotificationListener{})

	// 创建订单流程
	ctx := context.WithValue(context.Background(), "userID", "user-123")
	ctx = context.WithValue(ctx, "requestID", "req-456")

	fmt.Println("=== 创建订单 ===")
	dispatcher.PublishContext(ctx, OrderCreatedEvent{
		OrderID: "order-001",
		UserID:  "user-123",
		Amount:  199.99,
	})

	time.Sleep(100 * time.Millisecond)

	fmt.Println("\n=== 支付订单 ===")
	dispatcher.PublishContext(ctx, OrderPaidEvent{
		OrderID: "order-001",
		Amount:  199.99,
	})

	// 等待异步处理完成
	time.Sleep(200 * time.Millisecond)
}
```

---

## 总结

EventBus 的 context 支持让你可以：

1. **传递请求级别的数据** - 如用户 ID、请求 ID、认证信息等
2. **控制超时** - 防止监听器执行时间过长
3. **取消操作** - 在用户取消请求时及时停止处理
4. **中间件增强** - 在中间件中访问和修改 context
5. **异步处理** - 使用 worker 池提高性能

这些特性让 EventBus 更加强大和灵活，适合构建复杂的事件驱动系统。
