package selection

import (
	"bytes"
	"fmt"
	"time"

	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire/topics"
)

type (
	// collector is the collector of SigSetEvents. It manages the EventQueue and the Selector to pick the best signature for a given step
	collector struct {
		CurrentRound     uint64
		CurrentStep      uint8
		CurrentBlockHash []byte
		BestEventChan    chan *bytes.Buffer
		// this is the dump of future messages, categorized by different steps and different rounds
		queue     *consensus.EventQueue
		timeOut   time.Duration
		selector  *wire.EventSelector
		handler   consensus.EventHandler
		completed bool
	}

	selectionCollector struct {
		selectionChan chan bool
	}
)

func (sc *selectionCollector) Collect(r *bytes.Buffer) error {
	sc.selectionChan <- true
	return nil
}

// NewCollector creates a new Collector
func newCollector(bestEventChan chan *bytes.Buffer, handler consensus.EventHandler, timerLength time.Duration) *collector {
	return &collector{
		BestEventChan: bestEventChan,
		selector:      nil,
		handler:       handler,
		queue:         &consensus.EventQueue{},
		timeOut:       timerLength,
	}
}

// initCollector instantiates a Collector and triggers the related EventSubscriber. It is a helper method for those needing access to the Collector
func initCollector(handler consensus.EventHandler, timeout time.Duration, eventBus *wire.EventBus) *collector {
	bestEventChan := make(chan *bytes.Buffer, 1)
	collector := newCollector(bestEventChan, handler, timeout)
	go wire.NewEventSubscriber(eventBus, collector, string(topics.Score)).Accept()
	return collector
}

// Collect as specified in the EventCollector interface. It delegates unmarshalling to the SigSetUnMarshaller which also validates the signatures.
func (s *collector) Collect(r *bytes.Buffer) error {
	ev := s.handler.NewEvent()

	if err := s.handler.Unmarshal(r, ev); err != nil {
		return err
	}

	// verify
	if err := s.handler.Verify(ev); err != nil {
		return err
	}

	s.process(ev)
	return nil
}

// process sends SigSetEvent to the selector. The messages have already been validated and verified
func (s *collector) process(ev wire.Event) {
	h := &consensus.EventHeader{}
	s.handler.ExtractHeader(ev, h)

	// Not anymore
	if s.isObsolete(h.Round, h.Step) {
		return
	}

	// Not yet
	if s.isEarly(h.Round, h.Step) {
		s.queue.PutEvent(h.Round, h.Step, ev)
		return
	}

	// Just right :)
	s.selector.EventChan <- ev
}

func (s *collector) isObsolete(round uint64, step uint8) bool {
	return round < s.CurrentRound || step < s.CurrentStep
}

func (s *collector) isEarly(round uint64, step uint8) bool {
	return round > s.CurrentRound || step > s.CurrentStep || s.selector == nil
}

// UpdateRound stops the selection and cleans up the queue up to the round specified.
func (s *collector) UpdateRound(round uint64) {
	s.CurrentRound = round
	s.CurrentStep = 1
	s.stopSelection()

	// TODO: remove this and think of a better solution after the demo
	s.completed = false
	s.queue.ConsumeUntil(s.CurrentRound)
}

// StartSelection starts the selection of the best SigSetEvent. It also consumes stored events related to the current step
func (s *collector) StartSelection() {
	// TODO: remove
	fmt.Println("starting selection")
	// creating a new selector
	s.selector = wire.NewEventSelector(s.handler)
	// letting selector collect the best
	go s.selector.PickBest()
	// stopping the selector after timeout
	time.AfterFunc(s.timeOut, func() {
		s.selector.StopChan <- true
	})
	// listening to the selector and collect its pick
	go s.listenSelection()

	// flushing the queue and processing relevant events
	queued, step := s.queue.ConsumeNextStepEvents(s.CurrentRound)
	if step == s.CurrentStep {
		for _, ev := range queued {
			s.process(ev)
		}
	}
}

// listenSelection triggers a goroutine that notifies the Broker through its channel after having incremented the Collector step
func (s *collector) listenSelection() {
	ev := <-s.selector.BestEventChan
	buf := new(bytes.Buffer)
	if err := s.handler.Marshal(buf, ev); err == nil {
		s.BestEventChan <- buf
		return
	}
	s.BestEventChan <- buf
}

// stopSelection notifies the Selector to stop selecting
func (s *collector) stopSelection() {
	if s.selector != nil {
		s.selector.StopChan <- true
		s.selector = nil
	}
}