Go 实现的轻量级的事件管理、调度工具库

- 支持自定义事件对象
- 支持同时监听多个事件


## 主要方法


- `Subscribe(listener Listener) `  订阅，支持注册多个事件监听
- `Publish(event Event) ` 触发事件


## 快速使用

```shell
go get -u -v github.com/lianglong/EventBus
```

```go
package main

import (
	"fmt"
	"reflect"
	"github.com/lianglong/EventBus"
)

var (
	orderEventName  string
	refundEventName string
)

func init() {
	orderEventRef := reflect.TypeOf(OrderEvent{})
	orderEventName = fmt.Sprintf("%s.%s", orderEventRef.PkgPath(), orderEventRef.Name())
	refundEventRef := reflect.TypeOf(RefundEvent{})
	refundEventName = fmt.Sprintf("%s.%s", refundEventRef.PkgPath(), refundEventRef.Name())
}

var eventDispatcher *Dispatcher = EventBus.New()

type OrderEvent struct{ OrderID string }

func (OrderEvent) EventName() string { return orderEventName }

type RefundEvent struct{ OrderID string }

func (RefundEvent) EventName() string { return refundEventName }

// 统一监听器
type OrderListener struct{}

func (OrderListener) ListenEvents() []Event {
	return []Event{OrderEvent{}, RefundEvent{}}
}
func (OrderListener) Priority() int32 { return 1 }

func (OrderListener) Handle(e Event) error {
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

func main() {
	eventDispatcher := EventBus.New()
	// 注册事件监听器
	eventDispatcher.Subscribe(OrderListener{})
	
	// ... ...
	
	// 触发事件
	eventDispatcher.Publish(OrderEvent{OrderID: 123})
	eventDispatcher.Publish(RefundEvent{OrderID: 234})
}
```


## LICENSE

**[MIT](LICENSE)**