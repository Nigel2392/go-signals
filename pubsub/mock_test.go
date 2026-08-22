package pubsub

import (
	"context"
	"sync"
)

type MockSubscriber struct {
	mu     sync.Mutex
	ch     chan *Message
	closed bool
}

func NewMockSubscriber(ch chan *Message) *MockSubscriber {
	if ch == nil {
		ch = make(chan *Message, 100)
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
		if !ok || msg == nil {
			return nil, false
		}
		return msg.Data, true
	default:
		return nil, false
	}
}

func (m *MockSubscriber) push(data []byte, topic string) {
	m.ch <- &Message{
		Channel: topic,
		Data:    data,
	}
}

type MockPubSub struct {
	mu          sync.Mutex
	publish     chan *Message
	subscribers map[string][]*MockSubscriber
	PublishErr  error
	SubErr      error
}

func NewMockPubSub(async bool) *MockPubSub {
	var ch chan *Message
	if !async {
		ch = make(chan *Message, 100)
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
		m.publish <- &Message{
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

func (m *MockPubSub) Subscribe(ctx context.Context, topic string) (Subscriber, error) {
	if m.SubErr != nil {
		return nil, m.SubErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sub := NewMockSubscriber(m.publish)
	m.subscribers[topic] = append(m.subscribers[topic], sub)
	return sub, nil
}

func (m *MockPubSub) BindChannel(binder ChannelBinder) {
	if m.publish != nil {
		binder.SetChannel(m.publish)
	}
}
