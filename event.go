package EventBus

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	LowPriority    int32 = iota // 0 低优先级
	NormalPriority              // 1 普通优先级
	HighPriority                // 2 高优先级
)

var (
	ErrDispatcherClosed = errors.New("dispatcher is closed")
	ErrPublishTimeout   = errors.New("publish timeout")
)

type Event interface {
	EventName() string
}

type HandleFunc func(context.Context, Event) error

type Listener interface {
	Handle(context.Context, Event) error
	ListenEvents() []Event
	Priority() int32
}

// Middleware 中间件函数类型（标准洋葱模型）
//
// 用法示例：
//
//	func LogMiddleware(next HandleFunc) HandleFunc {
//	    return func(ctx context.Context, e Event) error {
//	        log.Printf("before: %s", e.EventName())
//	        err := next(ctx, e)
//	        log.Printf("after: %s, err=%v", e.EventName(), err)
//	        return err
//	    }
//	}
type Middleware func(HandleFunc) HandleFunc

// Subscription 订阅句柄，用于取消订阅
type Subscription struct {
	id         uint64
	eventName  string
	dispatcher *Dispatcher
}

// Unsubscribe 取消订阅
func (s *Subscription) Unsubscribe() {
	s.dispatcher.unsubscribe(s.eventName, s.id)
}

type listenerItem struct {
	id       uint64
	fn       HandleFunc
	priority int32
}

// Options 配置选项
type Options struct {
	// AsyncWorkers 异步发布的 worker 数量
	// - 0: 同步模式，发布时阻塞等待所有 handler 执行完成
	// - >0: 异步模式，发布后立即返回，worker 池后台执行
	//
	// 建议值：
	// - CPU 密集型任务: runtime.NumCPU()
	// - I/O 密集型任务: runtime.NumCPU() * 2 ~ 10
	// - 混合型任务: runtime.NumCPU() * 1.5
	// - 低延迟要求: 5 ~ 20
	// - 高吞吐量: 50 ~ 200
	//
	// 注意：过大的值会增加内存开销和上下文切换成本
	AsyncWorkers int

	// PublishTimeout 发布超时时间，0 表示无超时
	//
	// 异步模式 (AsyncWorkers > 0) 时：
	// - 限制的是"任务入队"的等待时间
	// - 如果队列已满，等待超过此时间将返回 ErrPublishTimeout
	// - 不限制 handler 的实际执行时间
	//
	// 同步模式 (AsyncWorkers = 0) 时：
	// - 限制的是"所有 handler 执行完成"的总时间
	// - 超时后会取消正在执行的 handler（通过 context）
	// - 返回 context.DeadlineExceeded 错误
	PublishTimeout time.Duration

	// ErrorHandler 错误处理函数
	// 当 handler 返回错误时被调用，可用于日志记录、告警等
	ErrorHandler func(Event, error)

	// Middlewares 中间件列表（标准洋葱模型）
	// 按顺序声明，第一个中间件在最外层（最先进入，最后退出）
	// 例如 [A, B, C] 的执行顺序为：A → B → C → handler → C → B → A
	Middlewares []Middleware
}

func New(opts ...*Options) *Dispatcher {
	d := &Dispatcher{
		nextID: 1,
		closed: 0,
	}

	m := make(map[string][]listenerItem)
	d.val.Store(&m)

	if len(opts) > 0 && opts[0] != nil {
		d.options = *opts[0]
	}

	// 启动异步 worker
	if d.options.AsyncWorkers > 0 {
		// taskCh 缓冲大小 = workers * 10，关闭时通过 close(taskCh) 通知 worker 退出
		d.taskCh = make(chan asyncTask, d.options.AsyncWorkers*10)
		d.wg = &sync.WaitGroup{}
		for i := 0; i < d.options.AsyncWorkers; i++ {
			d.wg.Add(1)
			go d.worker()
		}
	}

	return d
}

type Dispatcher struct {
	val     atomic.Value
	mu      sync.Mutex // 仅用于写操作（Subscribe/Unsubscribe），读路径仍无锁
	nextID  uint64
	options Options

	// 异步发布相关
	taskCh chan asyncTask
	wg     *sync.WaitGroup
	closed uint32
}

type asyncTask struct {
	event Event
	items []listenerItem
	ctx   context.Context
}

// worker 通过 range 消费 taskCh，taskCh 关闭后自然退出
// 这样 Close() 可以通过 close(taskCh) → wg.Wait() 优雅排空队列
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for task := range d.taskCh {
		d.executeListeners(task.ctx, task.event, task.items)
	}
}

// buildChain 将中间件链应用到 handler，返回最终的处理函数
// 执行顺序：Middlewares[0] → Middlewares[1] → ... → handler
func (d *Dispatcher) buildChain(handler HandleFunc) HandleFunc {
	// 逆序包裹，使 Middlewares[0] 在最外层
	for i := len(d.options.Middlewares) - 1; i >= 0; i-- {
		handler = d.options.Middlewares[i](handler)
	}
	return handler
}

// executeListeners 执行所有监听器，应用中间件并处理错误
func (d *Dispatcher) executeListeners(ctx context.Context, event Event, items []listenerItem) {
	for _, l := range items {
		// 每次循环前检查 context，支持超时/取消提前退出
		select {
		case <-ctx.Done():
			return
		default:
		}

		handler := d.buildChain(l.fn)

		if err := handler(ctx, event); err != nil {
			if d.options.ErrorHandler != nil {
				d.options.ErrorHandler(event, err)
			}
		}
	}
}

// addSubscribe 内部订阅实现
// 写操作持有 mu，保证并发 Subscribe 不会互相覆盖；读路径不需要锁
func (d *Dispatcher) addSubscribe(name string, fn HandleFunc, priority int32) (*Subscription, error) {
	if atomic.LoadUint32(&d.closed) == 1 {
		return nil, ErrDispatcherClosed
	}

	id := atomic.AddUint64(&d.nextID, 1)

	d.mu.Lock()
	defer d.mu.Unlock()

	// 在锁内执行 Load → 复制 → Store，避免并发写互相覆盖
	oldPtr := d.val.Load().(*map[string][]listenerItem)

	newMap := make(map[string][]listenerItem, len(*oldPtr)+1)
	for k, v := range *oldPtr {
		if k == name {
			newSlice := make([]listenerItem, len(v), len(v)+1)
			copy(newSlice, v)
			newMap[k] = newSlice
		} else {
			// 其他事件的切片不需要修改，直接共享
			newMap[k] = v
		}
	}

	newMap[name] = append(newMap[name], listenerItem{id: id, fn: fn, priority: priority})

	sort.SliceStable(newMap[name], func(i, j int) bool {
		return newMap[name][i].priority > newMap[name][j].priority
	})

	d.val.Store(&newMap)

	return &Subscription{
		id:         id,
		eventName:  name,
		dispatcher: d,
	}, nil
}

// unsubscribe 取消订阅（同样持锁写）
func (d *Dispatcher) unsubscribe(eventName string, id uint64) {
	if atomic.LoadUint32(&d.closed) == 1 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	oldPtr := d.val.Load().(*map[string][]listenerItem)
	newMap := make(map[string][]listenerItem, len(*oldPtr))

	for k, v := range *oldPtr {
		if k == eventName {
			newSlice := make([]listenerItem, 0, len(v))
			for _, item := range v {
				if item.id != id {
					newSlice = append(newSlice, item)
				}
			}
			if len(newSlice) > 0 {
				newMap[k] = newSlice
			}
		} else {
			newMap[k] = v
		}
	}

	d.val.Store(&newMap)
}

// Subscribe 订阅监听器，返回订阅句柄列表
// 若 dispatcher 已关闭，返回空列表（通过 error 通知调用方）
func (d *Dispatcher) Subscribe(listener Listener) ([]*Subscription, error) {
	events := listener.ListenEvents()
	if len(events) == 0 {
		return nil, nil
	}

	subs := make([]*Subscription, 0, len(events))
	for _, event := range events {
		sub, err := d.addSubscribe(event.EventName(), listener.Handle, listener.Priority())
		if err != nil {
			return subs, err
		}
		subs = append(subs, sub)
	}
	return subs, nil
}

// SubscribeFunc 订阅函数（便捷方法）
func (d *Dispatcher) SubscribeFunc(eventName string, fn HandleFunc, priority int32) (*Subscription, error) {
	return d.addSubscribe(eventName, fn, priority)
}

// Publish 同步发布事件
func (d *Dispatcher) Publish(e Event) error {
	return d.PublishContext(context.Background(), e)
}

// PublishContext 带上下文的发布，支持超时控制
func (d *Dispatcher) PublishContext(ctx context.Context, e Event) error {
	if atomic.LoadUint32(&d.closed) == 1 {
		return ErrDispatcherClosed
	}

	m := d.val.Load().(*map[string][]listenerItem)
	items := (*m)[e.EventName()]

	if len(items) == 0 {
		return nil
	}

	// 异步模式：将任务投入 worker 队列后立即返回
	if d.options.AsyncWorkers > 0 {
		task := asyncTask{event: e, items: items, ctx: ctx}

		if d.options.PublishTimeout > 0 {
			// PublishTimeout 在异步模式下限制的是"入队等待"时间，而非 handler 执行时间
			// 场景：队列已满（所有 worker 繁忙），此处阻塞超过 PublishTimeout 则返回 ErrPublishTimeout
			timer := time.NewTimer(d.options.PublishTimeout)
			defer timer.Stop()

			select {
			case d.taskCh <- task:
				return nil
			case <-timer.C:
				return ErrPublishTimeout
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		select {
		case d.taskCh <- task:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 同步模式：在当前 goroutine 直接执行，无需额外 goroutine
	if d.options.PublishTimeout > 0 {
		// PublishTimeout 在同步模式下限制的是"所有 handler 执行完毕"的总时间
		// handler 可通过 ctx.Done() 感知超时并提前退出
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.options.PublishTimeout)
		defer cancel()
	}

	// 直接调用，避免原版不必要的 goroutine + channel 开销
	d.executeListeners(ctx, e, items)

	// 超时/取消时返回对应错误
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

// Close 优雅关闭
//
// 异步模式：先关闭 taskCh，worker 排空队列后自然退出，再等待所有 worker 完成
// 同步模式：直接标记关闭，已在飞行中的 Publish 调用不受影响
func (d *Dispatcher) Close() error {
	if !atomic.CompareAndSwapUint32(&d.closed, 0, 1) {
		return ErrDispatcherClosed
	}

	// 先关闭 taskCh，worker 通过 range 排空剩余任务后自然退出
	// 注意顺序：必须先 close(taskCh) 再 wg.Wait()，反之 worker 不会退出
	if d.taskCh != nil {
		close(d.taskCh)
	}

	if d.wg != nil {
		d.wg.Wait()
	}

	return nil
}

// IsClosed 检查是否已关闭
func (d *Dispatcher) IsClosed() bool {
	return atomic.LoadUint32(&d.closed) == 1
}

// ListenerCount 获取指定事件的监听器数量
func (d *Dispatcher) ListenerCount(eventName string) int {
	m := d.val.Load().(*map[string][]listenerItem)
	return len((*m)[eventName])
}

// EventNames 获取所有已注册的事件名称
func (d *Dispatcher) EventNames() []string {
	m := d.val.Load().(*map[string][]listenerItem)
	names := make([]string, 0, len(*m))
	for name := range *m {
		names = append(names, name)
	}
	return names
}
