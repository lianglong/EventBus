package EventBus

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
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

var eventDispatcher *Dispatcher = New()

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

func TestEventSubscribe(t *testing.T) {
	eventDispatcher.Subscribe(OrderListener{})
}

func TestEventPublic(t *testing.T) {
	var wg sync.WaitGroup
	var num = 1000
	wg.Add(num)
	for i := 0; i < num; i++ {
		go func(n int) {
			defer wg.Done()
			orderId := fmt.Sprintf("%d", n)
			eventDispatcher.Publish(OrderEvent{OrderID: orderId})
			eventDispatcher.Publish(RefundEvent{OrderID: orderId})
		}(i)
	}
	wg.Wait()
}

func BenchmarkEventSubscribe(t *testing.B) {
	eventDispatcher.Subscribe(OrderListener{})
}

func BenchmarkEventPublic(t *testing.B) {
	var wg sync.WaitGroup
	var num = 1000
	wg.Add(num)
	for i := 0; i < num; i++ {
		go func(n int) {
			defer wg.Done()
			orderId := fmt.Sprintf("%d", n)
			eventDispatcher.Publish(OrderEvent{OrderID: orderId})
			eventDispatcher.Publish(RefundEvent{OrderID: orderId})
		}(i)
	}
	wg.Wait()
}
