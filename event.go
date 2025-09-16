package EventBus

import "sync/atomic"

const (
	LowPriority    int = iota //0 低优先级
	NormalPriority            //1 普通优先级
	HighPriority              //2 高优先级
)

type Event interface {
	EventName() string
}

type HandleFunc func(Event) error

type Listener interface {
	Handle(Event) error
	ListenEvents() []Event
	Priority() int32
}

type listenerItem struct {
	fn       HandleFunc
	priority int32
}

func New() *Dispatcher {
	d := &Dispatcher{}
	m := make(map[string][]listenerItem)
	d.val.Store(&m)
	return d
}

type Dispatcher struct {
	val atomic.Value
}

func (d *Dispatcher) addSubscribe(name string, fn HandleFunc, priority int32) error {
	// 1. 取出旧表
	oldPtr := d.val.Load().(*map[string][]listenerItem)
	// 2. 复制整张表（浅拷贝切片头）
	newMap := make(map[string][]listenerItem, len(*oldPtr))
	for k, v := range *oldPtr {
		newMap[k] = append([]listenerItem(nil), v...)
	}
	// 3. 追加新监听器
	l := listenerItem{fn: fn, priority: priority}
	newMap[name] = append(newMap[name], l)
	// 4. 按优先级简单排序（监听器数量通常 < 100）
	slice := newMap[name]
	for i := len(slice) - 1; i > 0 && slice[i].priority > slice[i-1].priority; i-- {
		slice[i], slice[i-1] = slice[i-1], slice[i]
	}
	// 5. 原子替换整张表
	d.val.Store(&newMap)
	return nil
}

func (d *Dispatcher) Subscribe(listener Listener) {
	events := listener.ListenEvents()
	if len(events) == 0 {
		return
	}
	for _, event := range events {
		d.addSubscribe(event.EventName(), listener.Handle, listener.Priority())
	}
}

func (d *Dispatcher) Publish(e Event) {
	m := d.val.Load().(*map[string][]listenerItem)
	for _, l := range (*m)[e.EventName()] {
		l.fn(e) // 直接函数指针调用，无 interface
	}
}
