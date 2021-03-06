package eventbus

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/dusk-network/dusk-blockchain/pkg/p2p/wire/topics"
	crypto "github.com/dusk-network/dusk-crypto/hash"
	"github.com/stretchr/testify/assert"
)

//*****************
// EVENTBUS TESTS
//*****************
func TestNewEventBus(t *testing.T) {
	eb := New()
	assert.NotNil(t, eb)
}

//******************
// SUBSCRIBER TESTS
//******************
func TestListenerMap(t *testing.T) {
	lm := newListenerMap()
	_, ss := CreateGossipStreamer()
	listener := NewStreamListener(ss)
	lm.Store(topics.Test, listener)

	listeners := lm.Load(topics.Test)
	assert.Equal(t, 1, len(listeners))
	assert.Equal(t, listener, listeners[0].Listener)
}

func TestSubscribe(t *testing.T) {
	eb := New()
	myChan := make(chan bytes.Buffer, 10)
	cl := NewChanListener(myChan)
	assert.NotNil(t, eb.Subscribe(topics.Test, cl))
}

func TestUnsubscribe(t *testing.T) {
	eb, myChan, id := newEB(t)
	eb.Unsubscribe(topics.Test, id)
	eb.Publish(topics.Test, bytes.NewBufferString("whatever2"))

	select {
	case <-myChan:
		assert.FailNow(t, "We should have not received message")
	case <-time.After(50 * time.Millisecond):
		// success
	}
}

//*********************
// STREAMER TESTS
//*********************
func TestStreamer(t *testing.T) {
	topic := topics.Gossip
	bus, streamer := CreateFrameStreamer(topic)
	bus.Publish(topic, bytes.NewBufferString("pluto"))

	packet, err := streamer.(*SimpleStreamer).Read()
	if !assert.NoError(t, err) {
		assert.FailNow(t, "error in reading from the subscribed stream")
	}

	// first 4 bytes of packet are the checksum
	assert.Equal(t, "pluto", string(packet[4:]))
}

//******************
// MULTICASTER TESTS
//******************
func TestDefaultListener(t *testing.T) {
	eb := New()
	msgChan := make(chan struct {
		topic topics.Topic
		buf   bytes.Buffer
	})

	cb := func(r bytes.Buffer) error {
		tpc, _ := topics.Extract(&r)

		msgChan <- struct {
			topic topics.Topic
			buf   bytes.Buffer
		}{tpc, r}
		return nil
	}

	eb.AddDefaultTopic(topics.Reject)
	eb.AddDefaultTopic(topics.Unknown)
	eb.SubscribeDefault(NewCallbackListener(cb))

	eb.Publish(topics.Reject, bytes.NewBufferString("pluto"))
	msg := <-msgChan
	assert.Equal(t, topics.Reject, msg.topic)
	assert.Equal(t, []byte("pluto"), msg.buf.Bytes())

	eb.Publish(topics.Unknown, bytes.NewBufferString("pluto"))
	msg = <-msgChan
	assert.Equal(t, topics.Unknown, msg.topic)
	assert.Equal(t, []byte("pluto"), msg.buf.Bytes())

	eb.Publish(topics.Gossip, bytes.NewBufferString("pluto"))
	select {
	case <-msgChan:
		t.FailNow()
	case <-time.After(100 * time.Millisecond):
		//all good
	}
}

//****************
// SETUP FUNCTIONS
//****************
func newEB(t *testing.T) (*EventBus, chan bytes.Buffer, uint32) {
	eb := New()
	myChan := make(chan bytes.Buffer, 10)
	cl := NewChanListener(myChan)
	id := eb.Subscribe(topics.Test, cl)
	assert.NotNil(t, id)
	b := bytes.NewBufferString("whatever")
	eb.Publish(topics.Test, b)

	select {
	case received := <-myChan:
		assert.Equal(t, "whatever", received.String())
	case <-time.After(50 * time.Millisecond):
		assert.FailNow(t, "We should have received a message by now")
	}

	return eb, myChan, id
}

// Test that a streaming goroutine is killed when the exit signal is sent
func TestExitChan(t *testing.T) {
	eb := New()
	topic := topics.Test
	sl := NewStreamListener(&mockWriteCloser{})
	_ = eb.Subscribe(topic, sl)

	// Put something on ring buffer
	val := new(bytes.Buffer)
	val.Write([]byte{0})
	eb.Publish(topic, val)
	// Wait for event to be handled
	// NB: 'Writer' must return error to force consumer termination
	time.Sleep(100 * time.Millisecond)

	l := eb.listeners.Load(topic)
	for _, listener := range l {
		if streamer, ok := listener.Listener.(*StreamListener); ok {
			if !assert.True(t, streamer.ringbuffer.Closed()) {
				assert.FailNow(t, "ringbuffer not closed")
			}
			return
		}
	}
	assert.FailNow(t, "stream listener not found")
}

func ranbuf() *bytes.Buffer {
	tbytes, _ := crypto.RandEntropy(32)
	return bytes.NewBuffer(tbytes)
}

type mockWriteCloser struct {
}

func (m *mockWriteCloser) Write(data []byte) (int, error) {
	return 0, errors.New("failed")
}

func (m *mockWriteCloser) Close() error {
	return nil
}
