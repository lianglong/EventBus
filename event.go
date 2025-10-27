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
	LowPriority    int32 = iota //0 低优先级
	NormalPriority              //1 普通优先级
	HighPriority                //2 高优先级
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

// Middleware 中间件函数类型
type Middleware func(context.Context, Event, HandleFunc) HandleFunc

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
	// 异步模式(AsyncWorkers > 0)时：
	// - 限制的是"任务入队"的等待时间
	// - 如果队列已满，等待超过此时间将返回 ErrPublishTimeout
	// - 不限制 handler 的实际执行时间
	//
	// 同步模式(AsyncWorkers = 0)时：
	// - 限制的是"所有 handler 执行完成"的总时间
	// - 超时后会取消正在执行的 handler（通过 context）
	// - 返回 context.DeadlineExceeded 错误
	PublishTimeout time.Duration

	// ErrorHandler 错误处理函数
	// 当 handler 返回错误时被调用，可用于日志记录、告警等
	ErrorHandler func(Event, error)

	// Middlewares 中间件列表
	// 按顺序应用，最后一个中间件最先执行（洋葱模型）
	Middlewares []Middleware
}

func New(opts ...*Options) *Dispatcher {
	d := &Dispatcher{
		nextID:  1,
		closed:  0,
		closeCh: make(chan struct{}),
	}

	m := make(map[string][]listenerItem)
	d.val.Store(&m)

	if len(opts) > 0 && opts[0] != nil {
		d.options = *opts[0]
	}

	// 启动异步 worker
	if d.options.AsyncWorkers > 0 {
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
	nextID  uint64
	options Options

	// 异步发布相关
	taskCh  chan asyncTask
	wg      *sync.WaitGroup
	closed  uint32
	closeCh chan struct{}
}

type asyncTask struct {
	event Event
	items []listenerItem
	ctx   context.Context
}

// worker 异步处理事件
func (d *Dispatcher) worker() {
	defer d.wg.Done()
	for {
		select {
		case <-d.closeCh:
			return
		case task := <-d.taskCh:
			d.executeListeners(task.ctx, task.event, task.items)
		}
	}
}

// executeListeners 执行所有监听器，应用中间件并处理错误
func (d *Dispatcher) executeListeners(ctx context.Context, event Event, items []listenerItem) {
	for _, l := range items {
		// 检查 context 是否已取消，如果取消则提前退出
		select {
		case <-ctx.Done():
			return
		default:
		}

		handler := l.fn

		// 应用中间件（逆序，最后一个中间件最先执行）
		for i := len(d.options.Middlewares) - 1; i >= 0; i-- {
			handler = d.options.Middlewares[i](ctx, event, handler)
		}

		// 执行处理器
		if err := handler(ctx, event); err != nil {
			if d.options.ErrorHandler != nil {
				d.options.ErrorHandler(event, err)
			}
		}
	}
}

func (d *Dispatcher) addSubscribe(name string, fn HandleFunc, priority int32) (*Subscription, error) {
	if atomic.LoadUint32(&d.closed) == 1 {
		return nil, ErrDispatcherClosed
	}

	// 生成唯一 ID
	id := atomic.AddUint64(&d.nextID, 1)

	// 1. 取出旧表
	oldPtr := d.val.Load().(*map[string][]listenerItem)

	// 2. 只复制需要修改的切片，优化内存分配
	newMap := make(map[string][]listenerItem, len(*oldPtr)+1)
	for k, v := range *oldPtr {
		if k == name {
			// 复制并预分配空间
			newMap[k] = make([]listenerItem, len(v), len(v)+1)
			copy(newMap[k], v)
		} else {
			// 其他事件直接共享切片
			newMap[k] = v
		}
	}

	// 3. 追加新监听器
	l := listenerItem{id: id, fn: fn, priority: priority}
	newMap[name] = append(newMap[name], l)

	// 4. 使用标准库排序（优化排序算法）
	sort.SliceStable(newMap[name], func(i, j int) bool {
		return newMap[name][i].priority > newMap[name][j].priority
	})

	// 5. 原子替换整张表
	d.val.Store(&newMap)

	return &Subscription{
		id:         id,
		eventName:  name,
		dispatcher: d,
	}, nil
}

// unsubscribe 取消订阅
func (d *Dispatcher) unsubscribe(eventName string, id uint64) {
	if atomic.LoadUint32(&d.closed) == 1 {
		return
	}

	oldPtr := d.val.Load().(*map[string][]listenerItem)
	newMap := make(map[string][]listenerItem, len(*oldPtr))

	for k, v := range *oldPtr {
		if k == eventName {
			// 过滤掉指定 ID 的监听器
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

// Subscribe 订阅监听器，返回订阅句柄
func (d *Dispatcher) Subscribe(listener Listener) []*Subscription {
	events := listener.ListenEvents()
	if len(events) == 0 {
		return nil
	}

	subs := make([]*Subscription, 0, len(events))
	for _, event := range events {
		sub, err := d.addSubscribe(event.EventName(), listener.Handle, listener.Priority())
		if err == nil && sub != nil {
			subs = append(subs, sub)
		}
	}
	return subs
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

	// 异步发布
	if d.options.AsyncWorkers > 0 {
		task := asyncTask{
			event: e,
			items: items,
			ctx:   ctx,
		}

		// 支持超时
		if d.options.PublishTimeout > 0 {
			// 【重要】这里的 timer 限制的是"任务入队"的等待时间
			// 场景：如果所有 worker 都在忙碌，且队列已满（容量 = AsyncWorkers * 10）
			//      此时 d.taskCh <- task 会阻塞等待队列有空位
			//      timer 防止在这里等待太久
			//
			// 注意：这里 NOT 限制 handler 的实际执行时间
			//      handler 执行时间由 worker 中的 ctx 控制
			timer := time.NewTimer(d.options.PublishTimeout)
			defer timer.Stop()

			select {
			case d.taskCh <- task:
				// 成功放入队列，立即返回（不等待执行完成）
				return nil
			case <-timer.C:
				// 入队超时：队列一直满，超过 PublishTimeout 还放不进去
				return ErrPublishTimeout
			case <-ctx.Done():
				// context 被取消（可能是用户主动取消或上层超时）
				return ctx.Err()
			}
		}

		// 没有配置 PublishTimeout，无限等待入队
		select {
		case d.taskCh <- task:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	// 同步发布
	if d.options.PublishTimeout > 0 {
		// 【重要】这里的 WithTimeout 限制的是"所有 handler 执行完成"的总时间
		// 场景：有 3 个 handler，每个执行 200ms，总共需要 600ms
		//      如果 PublishTimeout = 500ms，则第 3 个 handler 会被打断
		//
		// 机制：通过 context 传递超时信号
		//      - handler 可以通过 ctx.Done() 感知超时并提前退出
		//      - executeListeners 每次循环前也会检查 ctx.Done()
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.options.PublishTimeout)
		defer cancel()
	}

	// 启动 goroutine 执行所有监听器
	done := make(chan struct{})
	go func() {
		d.executeListeners(ctx, e, items)
		close(done)
	}()

	// 等待执行完成或超时
	select {
	case <-done:
		// 所有 handler 执行完成
		return nil
	case <-ctx.Done():
		// 超时或被取消
		// 注意：executeListeners 的 goroutine 还在运行，但会被 ctx.Done() 打断
		return ctx.Err()
	}
}

// Close 优雅关闭，等待所有事件处理完成
func (d *Dispatcher) Close() error {
	if !atomic.CompareAndSwapUint32(&d.closed, 0, 1) {
		return ErrDispatcherClosed
	}

	close(d.closeCh)

	// 等待所有 worker 完成
	if d.wg != nil {
		d.wg.Wait()
	}

	// 关闭任务通道
	if d.taskCh != nil {
		close(d.taskCh)
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
