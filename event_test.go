package EventBus

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 使用常量定义事件名称，避免反射开销
const (
	OrderEventName  = "order.created"
	RefundEventName = "order.refunded"
)

type OrderEvent struct{ OrderID string }

func (OrderEvent) EventName() string { return OrderEventName }

type RefundEvent struct{ OrderID string }

func (RefundEvent) EventName() string { return RefundEventName }

// 统一监听器
type OrderListener struct{}

func (OrderListener) ListenEvents() []Event {
	return []Event{OrderEvent{}, RefundEvent{}}
}
func (OrderListener) Priority() int32 { return 1 }

func (OrderListener) Handle(ctx context.Context, e Event) error {
	switch ev := e.(type) {
	case OrderEvent:
		fmt.Printf("[%s]收到支付%s\n", e.EventName(), ev.OrderID)
	case RefundEvent:
		fmt.Printf("[%s]收到退款%s\n", e.EventName(), ev.OrderID)
	default:
		// 忽略不关心的类型
	}
	return nil
}

// TestBasicSubscribePublish 测试基本的订阅和发布
func TestBasicSubscribePublish(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	dispatcher.Subscribe(OrderListener{})

	err := dispatcher.Publish(OrderEvent{OrderID: "123"})
	if err != nil {
		t.Errorf("Publish failed: %v", err)
	}
}

// TestContextValue 测试通过 context 传递值
func TestContextValue(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	var receivedUserID string
	var receivedRequestID string

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		if userID, ok := ctx.Value("userID").(string); ok {
			receivedUserID = userID
		}
		if requestID, ok := ctx.Value("requestID").(string); ok {
			receivedRequestID = requestID
		}
		return nil
	}, NormalPriority)

	// 通过 context 传递值
	ctx := context.WithValue(context.Background(), "userID", "user-123")
	ctx = context.WithValue(ctx, "requestID", "req-456")
	dispatcher.PublishContext(ctx, OrderEvent{OrderID: "order-1"})

	if receivedUserID != "user-123" {
		t.Errorf("Expected userID 'user-123', got '%s'", receivedUserID)
	}
	if receivedRequestID != "req-456" {
		t.Errorf("Expected requestID 'req-456', got '%s'", receivedRequestID)
	}
}

// TestContextCancellation 测试 context 取消
func TestContextCancellation(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	var handler1Called bool
	var handler2Called bool
	var handler3Called bool

	// 注册多个监听器，测试取消后后续监听器不会执行
	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		handler1Called = true
		// 第一个 handler 检查并返回取消错误
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}, HighPriority)

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		// 这个不应该被执行，因为 context 已取消
		handler2Called = true
		return nil
	}, NormalPriority)

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		// 这个也不应该被执行
		handler3Called = true
		return nil
	}, LowPriority)

	// 创建已取消的 context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	dispatcher.PublishContext(ctx, OrderEvent{OrderID: "order-1"})

	// 第一个 handler 会被调用但应该检测到取消
	if !handler1Called {
		t.Errorf("Handler1 should be called")
	}

	// 由于在 executeListeners 中检查了 ctx.Done()，后续 handler 不应该被调用
	if handler2Called {
		t.Errorf("Handler2 should not be called when context is cancelled")
	}
	if handler3Called {
		t.Errorf("Handler3 should not be called when context is cancelled")
	}
}

// TestContextTimeout 测试 context 超时
func TestContextTimeout(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	var completedHandlers int32

	// 注册一个耗时的监听器
	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
			atomic.AddInt32(&completedHandlers, 1)
			return nil
		}
	}, NormalPriority)

	// 设置 100ms 超时
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := dispatcher.PublishContext(ctx, OrderEvent{OrderID: "order-1"})

	// 应该返回超时错误
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected DeadlineExceeded error, got %v", err)
	}

	// handler 不应该完成
	time.Sleep(250 * time.Millisecond)
	if atomic.LoadInt32(&completedHandlers) != 0 {
		t.Errorf("Handler should not complete when context times out")
	}
}

// TestUnsubscribe 测试取消订阅
func TestUnsubscribe(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	subs := dispatcher.Subscribe(OrderListener{})
	if len(subs) != 2 {
		t.Errorf("Expected 2 subscriptions, got %d", len(subs))
	}

	// 取消订阅
	for _, sub := range subs {
		sub.Unsubscribe()
	}

	// 验证监听器已被移除
	if dispatcher.ListenerCount(OrderEventName) != 0 {
		t.Errorf("Expected 0 listeners after unsubscribe, got %d", dispatcher.ListenerCount(OrderEventName))
	}
}

// TestPriority 测试优先级排序
func TestPriority(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	var order []int
	var mu sync.Mutex

	// 注册不同优先级的监听器
	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		mu.Lock()
		order = append(order, 1)
		mu.Unlock()
		return nil
	}, LowPriority)

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		mu.Lock()
		order = append(order, 3)
		mu.Unlock()
		return nil
	}, HighPriority)

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		mu.Lock()
		order = append(order, 2)
		mu.Unlock()
		return nil
	}, NormalPriority)

	dispatcher.Publish(OrderEvent{OrderID: "123"})

	// 验证执行顺序：高优先级 -> 普通 -> 低优先级
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 || order[0] != 3 || order[1] != 2 || order[2] != 1 {
		t.Errorf("Expected order [3,2,1], got %v", order)
	}
}

// TestErrorHandling 测试错误处理
func TestErrorHandling(t *testing.T) {
	var capturedError error
	var capturedEvent Event

	dispatcher := New(&Options{
		ErrorHandler: func(e Event, err error) {
			capturedError = err
			capturedEvent = e
		},
	})
	defer dispatcher.Close()

	expectedErr := errors.New("test error")
	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		return expectedErr
	}, NormalPriority)

	event := OrderEvent{OrderID: "123"}
	dispatcher.Publish(event)

	if capturedError != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, capturedError)
	}
	if capturedEvent != event {
		t.Errorf("Expected event %v, got %v", event, capturedEvent)
	}
}

// TestMiddleware 测试中间件
func TestMiddleware(t *testing.T) {
	var called []string
	var mu sync.Mutex

	middleware1 := func(ctx context.Context, e Event, next HandleFunc) HandleFunc {
		return func(ctx context.Context, event Event) error {
			mu.Lock()
			called = append(called, "before1")
			mu.Unlock()
			err := next(ctx, event)
			mu.Lock()
			called = append(called, "after1")
			mu.Unlock()
			return err
		}
	}

	middleware2 := func(ctx context.Context, e Event, next HandleFunc) HandleFunc {
		return func(ctx context.Context, event Event) error {
			mu.Lock()
			called = append(called, "before2")
			mu.Unlock()
			err := next(ctx, event)
			mu.Lock()
			called = append(called, "after2")
			mu.Unlock()
			return err
		}
	}

	dispatcher := New(&Options{
		Middlewares: []Middleware{middleware1, middleware2},
	})
	defer dispatcher.Close()

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		mu.Lock()
		called = append(called, "handler")
		mu.Unlock()
		return nil
	}, NormalPriority)

	dispatcher.Publish(OrderEvent{OrderID: "123"})

	expected := []string{"before2", "before1", "handler", "after1", "after2"}
	mu.Lock()
	defer mu.Unlock()
	if len(called) != len(expected) {
		t.Errorf("Expected %d calls, got %d: %v", len(expected), len(called), called)
	}
	for i, v := range expected {
		if i >= len(called) || called[i] != v {
			t.Errorf("At position %d: expected %s, got %v", i, v, called)
		}
	}
}

// TestAsyncPublish 测试异步发布
func TestAsyncPublish(t *testing.T) {
	var counter int32
	dispatcher := New(&Options{
		AsyncWorkers: 5,
	})
	defer dispatcher.Close()

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&counter, 1)
		return nil
	}, NormalPriority)

	// 发布多个事件
	for i := 0; i < 100; i++ {
		err := dispatcher.Publish(OrderEvent{OrderID: fmt.Sprintf("%d", i)})
		if err != nil {
			t.Errorf("Publish failed: %v", err)
		}
	}

	// 关闭并等待
	dispatcher.Close()

	if atomic.LoadInt32(&counter) != 100 {
		t.Errorf("Expected 100 events processed, got %d", counter)
	}
}

// TestPublishTimeout 测试发布超时
func TestPublishTimeout(t *testing.T) {
	dispatcher := New(&Options{
		PublishTimeout: 50 * time.Millisecond,
	})
	defer dispatcher.Close()

	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		time.Sleep(200 * time.Millisecond)
		return nil
	}, NormalPriority)

	err := dispatcher.Publish(OrderEvent{OrderID: "123"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Expected timeout error, got %v", err)
	}
}

// TestClose 测试优雅关闭
func TestClose(t *testing.T) {
	dispatcher := New()

	err := dispatcher.Close()
	if err != nil {
		t.Errorf("Close failed: %v", err)
	}

	// 关闭后不能发布
	err = dispatcher.Publish(OrderEvent{OrderID: "123"})
	if !errors.Is(err, ErrDispatcherClosed) {
		t.Errorf("Expected ErrDispatcherClosed, got %v", err)
	}

	// 重复关闭应该返回错误
	err = dispatcher.Close()
	if !errors.Is(err, ErrDispatcherClosed) {
		t.Errorf("Expected ErrDispatcherClosed on second close, got %v", err)
	}
}

// TestConcurrentPublish 测试并发发布
func TestConcurrentPublish(t *testing.T) {
	dispatcher := New()
	defer dispatcher.Close()

	var counter int32
	dispatcher.SubscribeFunc(OrderEventName, func(ctx context.Context, e Event) error {
		atomic.AddInt32(&counter, 1)
		return nil
	}, NormalPriority)

	var wg sync.WaitGroup
	numGoroutines := 100
	eventsPerGoroutine := 10

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(n int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				dispatcher.Publish(OrderEvent{OrderID: fmt.Sprintf("%d-%d", n, j)})
			}
		}(i)
	}
	wg.Wait()

	expected := int32(numGoroutines * eventsPerGoroutine)
	if atomic.LoadInt32(&counter) != expected {
		t.Errorf("Expected %d events, got %d", expected, counter)
	}
}

// BenchmarkPublishSync 同步发布性能测试
func BenchmarkPublishSync(b *testing.B) {
	dispatcher := New()
	defer dispatcher.Close()

	dispatcher.Subscribe(OrderListener{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatcher.Publish(OrderEvent{OrderID: "123"})
	}
}

// BenchmarkPublishAsync 异步发布性能测试
func BenchmarkPublishAsync(b *testing.B) {
	dispatcher := New(&Options{
		AsyncWorkers: 10,
	})
	defer dispatcher.Close()

	dispatcher.Subscribe(OrderListener{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatcher.Publish(OrderEvent{OrderID: "123"})
	}
}

// BenchmarkSubscribe 订阅性能测试
func BenchmarkSubscribe(b *testing.B) {
	dispatcher := New()
	defer dispatcher.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		dispatcher.Subscribe(OrderListener{})
	}
}

// BenchmarkConcurrentPublish 并发发布性能测试
func BenchmarkConcurrentPublish(b *testing.B) {
	dispatcher := New()
	defer dispatcher.Close()

	dispatcher.Subscribe(OrderListener{})

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			dispatcher.Publish(OrderEvent{OrderID: "123"})
		}
	})
}
