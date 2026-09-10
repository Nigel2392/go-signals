package pubsub2

import (
	"context"
	"sync"

	"github.com/Nigel2392/go-signals/pubsub"
)

type MockSubscriber struct {
	mu     sync.Mutex
	ch     chan pubsub.Message
	closed bool
}

func NewMockSubscriber(ch chan pubsub.Message) *MockSubscriber {
	if ch == nil {
		ch = make(chan pubsub.Message, 100)
	}
	return &MockSubscriber{
		ch: ch,
	}
}

func (m *MockSubscriber) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func (m *MockSubscriber) TryReceive() ([]byte, bool) {
	select {
	case msg, ok := <-m.ch:
		if !ok {
			return nil, false
		}
		return msg.Data, true
	default:
		return nil, false
	}
}

func (m *MockSubscriber) push(data []byte, topic string) {
	m.ch <- pubsub.Message{
		Channel: topic,
		Data:    data,
	}
}

type MockPubSub struct {
	mu          sync.Mutex
	publish     chan pubsub.Message
	subscribers map[string][]*MockSubscriber
	PublishErr  error
	SubErr      error
}

func NewMockPubSub(async bool) *MockPubSub {
	var ch chan pubsub.Message
	if !async {
		ch = make(chan pubsub.Message, 100)
	}

	return &MockPubSub{
		publish:     ch,
		subscribers: make(map[string][]*MockSubscriber),
	}
}

func (m *MockPubSub) Publish(ctx context.Context, topic string, data []byte) error {
	if m.PublishErr != nil {
		return m.PublishErr
	}

	if m.publish != nil {
		m.publish <- pubsub.Message{
			Channel: topic,
			Data:    data,
		}
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if subs, ok := m.subscribers[topic]; ok {
		for _, sub := range subs {
			sub.push(data, topic)
		}
	}
	return nil
}

func (m *MockPubSub) Subscribe(ctx context.Context, topic string) (pubsub.Subscriber, error) {
	if m.SubErr != nil {
		return nil, m.SubErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sub := NewMockSubscriber(m.publish)
	m.subscribers[topic] = append(m.subscribers[topic], sub)
	return sub, nil
}

func (m *MockPubSub) BindChannel(ctx context.Context, binder pubsub.ChannelBinder) {
	if m.publish != nil {
		binder.SetChannel(ctx, m.publish)
	}
}
