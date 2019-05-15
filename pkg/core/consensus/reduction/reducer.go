package reduction

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/events"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/msg"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire/encoding"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire/topics"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/util/nativeutils/sortedset"
	"golang.org/x/crypto/ed25519"
)

// LaunchNotification is a helper function allowing node internal processes interested in reduction messages to receive Reduction events as they get produced
func LaunchNotification(eventbus wire.EventSubscriber) <-chan *events.Reduction {
	revChan := make(chan *events.Reduction)
	evChan := consensus.LaunchNotification(eventbus,
		events.NewOutgoingReductionUnmarshaller(), msg.OutgoingBlockReductionTopic)

	go func() {
		for {
			rEv := <-evChan
			reductionEvent := rEv.(*events.Reduction)
			revChan <- reductionEvent
		}
	}()

	return revChan
}

var empty struct{}

type eventStopWatch struct {
	collectedVotesChan chan []wire.Event
	stopChan           chan struct{}
	timer              *consensus.Timer
}

func newEventStopWatch(collectedVotesChan chan []wire.Event, timer *consensus.Timer) *eventStopWatch {
	return &eventStopWatch{
		collectedVotesChan: collectedVotesChan,
		stopChan:           make(chan struct{}),
		timer:              timer,
	}
}

func (esw *eventStopWatch) fetch() []wire.Event {
	timer := time.NewTimer(esw.timer.Timeout)
	select {
	case <-timer.C:
		return nil
	case collectedVotes := <-esw.collectedVotesChan:
		timer.Stop()
		return collectedVotes
	case <-esw.stopChan:
		timer.Stop()
		return nil
	}
}

func (esw *eventStopWatch) stop() {
	select {
	case esw.stopChan <- empty:
	default:
	}
}

type reducer struct {
	firstStep   *eventStopWatch
	secondStep  *eventStopWatch
	ctx         *context
	accumulator *consensus.Accumulator

	sync.RWMutex
	stale bool

	publisher wire.EventPublisher
}

func newReducer(collectedVotesChan chan []wire.Event, ctx *context,
	publisher wire.EventPublisher, accumulator *consensus.Accumulator) *reducer {
	return &reducer{
		ctx:         ctx,
		firstStep:   newEventStopWatch(collectedVotesChan, ctx.timer),
		secondStep:  newEventStopWatch(collectedVotesChan, ctx.timer),
		publisher:   publisher,
		accumulator: accumulator,
	}
}

func (r *reducer) inCommittee() bool {
	round := r.ctx.state.Round()
	step := r.ctx.state.Step()
	return r.ctx.committee.AmMember(round, step)
}

func (r *reducer) startReduction(hash []byte) {
	log.Traceln("Starting Reduction")
	r.Lock()
	r.stale = false
	r.Unlock()
	r.sendReduction(bytes.NewBuffer(hash))
	go r.begin()
}

func (r *reducer) begin() {
	log.WithField("process", "reducer").Traceln("Beginning Reduction")
	events := r.firstStep.fetch()
	log.WithField("process", "reducer").Traceln("First step completed")
	hash1 := r.extractHash(events)
	r.RLock()
	if !r.stale {
		// if there was a timeout, we should report nodes that did not vote
		if events == nil {
			_ = r.ctx.committee.ReportAbsentees(r.accumulator.All(),
				r.ctx.state.Round(), r.ctx.state.Step())
		}
		r.ctx.state.IncrementStep()
		if r.inCommittee() {
			r.sendReduction(hash1)
		}
	}
	r.RUnlock()

	eventsSecondStep := r.secondStep.fetch()
	log.WithField("process", "reducer").Traceln("Second step completed")
	r.RLock()
	defer r.RUnlock()
	if !r.stale {
		if eventsSecondStep == nil {
			_ = r.ctx.committee.ReportAbsentees(r.accumulator.All(),
				r.ctx.state.Round(), r.ctx.state.Step())
		}
		hash2 := r.extractHash(eventsSecondStep)
		allEvents := append(events, eventsSecondStep...)
		if r.isReductionSuccessful(hash1, hash2) && r.inCommittee() {
			log.WithFields(log.Fields{
				"process":    "reducer",
				"votes":      len(allEvents),
				"block hash": hex.EncodeToString(hash1.Bytes()),
			}).Debugln("Reduction successful")
			r.sendAgreement(allEvents, hash2)
		}

		r.ctx.state.IncrementStep()
		r.publishRegeneration()
	}
}

func (r *reducer) sendReduction(hash *bytes.Buffer) {
	vote := new(bytes.Buffer)

	h := &events.Header{
		PubKeyBLS: r.ctx.Keys.BLSPubKeyBytes,
		Round:     r.ctx.state.Round(),
		Step:      r.ctx.state.Step(),
		BlockHash: hash.Bytes(),
	}

	if err := events.MarshalSignableVote(vote, h); err != nil {
		logErr(err, hash.Bytes(), "Error during marshalling of the reducer vote")
		return
	}

	if err := events.SignReduction(vote, r.ctx.Keys); err != nil {
		logErr(err, hash.Bytes(), "Error while signing vote")
		return
	}

	message, err := wire.AddTopic(vote, topics.Reduction)
	if err != nil {
		logErr(err, hash.Bytes(), "Error while adding topic")
		return
	}

	r.publisher.Publish(string(topics.Gossip), message)
}

func logErr(err error, hash []byte, msg string) {
	log.WithFields(
		log.Fields{
			"process":    "reducer",
			"block hash": hash,
		}).WithError(err).Errorln(msg)
}

func (r *reducer) sendAgreement(evs []wire.Event, hash *bytes.Buffer) {
	h := &events.Header{
		PubKeyBLS: r.ctx.BLSPubKeyBytes,
		Round:     r.ctx.state.Round(),
		Step:      r.ctx.state.Step(),
		BlockHash: hash.Bytes(),
	}

	// create the Agreement event
	a, err := r.Aggregate(h, evs)
	if err != nil {
		logErr(err, hash.Bytes(), "Error in creating the Agreement event")
		return
	}

	// BLS sign it
	if err := events.SignAgreementEvent(a, r.ctx.Keys); err != nil {
		logErr(err, hash.Bytes(), "Error in signing the Agreement")
		return
	}

	// Marshall it for Ed25519 signature
	buffer, err := events.MarshalAggregatedAgreement(a)
	if err != nil {
		logErr(err, hash.Bytes(), "Error in Marshalling the Agreement")
		return
	}

	// sign the whole message
	signed := r.signEd25519(buffer)
	//add the topic
	message, err := wire.AddTopic(signed, topics.Agreement)
	if err != nil {
		logErr(err, hash.Bytes(), "Error in adding topic to the Agreement")
		return
	}

	//send it
	r.publisher.Publish(string(topics.Gossip), message)
}

func (r *reducer) signEd25519(eventBuf *bytes.Buffer) *bytes.Buffer {
	signature := ed25519.Sign(*r.ctx.EdSecretKey, eventBuf.Bytes())
	buf := new(bytes.Buffer)
	if err := encoding.Write512(buf, signature); err != nil {
		panic(err)
	}

	if err := encoding.Write256(buf, r.ctx.EdPubKeyBytes); err != nil {
		panic(err)
	}

	if _, err := buf.Write(eventBuf.Bytes()); err != nil {
		panic(err)
	}

	return buf
}

func (r *reducer) publishRegeneration() {
	roundAndStep := make([]byte, 8)
	binary.LittleEndian.PutUint64(roundAndStep, r.ctx.state.Round())
	roundAndStep = append(roundAndStep, byte(r.ctx.state.Step()))
	r.publisher.Publish(msg.BlockRegenerationTopic, bytes.NewBuffer(roundAndStep))
}

func (r *reducer) isReductionSuccessful(hash1, hash2 *bytes.Buffer) bool {
	bothNotNil := !bytes.Equal(hash1.Bytes(), make([]byte, 32)) &&
		!bytes.Equal(hash2.Bytes(), make([]byte, 32))
	identicalResults := bytes.Equal(hash1.Bytes(), hash2.Bytes())
	return bothNotNil && identicalResults
}

func (r *reducer) end() {
	r.Lock()
	defer r.Unlock()
	r.stale = true
	r.firstStep.stop()
	r.secondStep.stop()
}

func (r *reducer) extractHash(events []wire.Event) *bytes.Buffer {
	if events == nil {
		return bytes.NewBuffer(make([]byte, 32))
	}

	hash := new(bytes.Buffer)
	if err := r.ctx.handler.ExtractIdentifier(events[0], hash); err != nil {
		panic(err)
	}
	return hash
}

// Aggregate the Agreement event into an AggregatedAgreement outgoing event
func (r *reducer) Aggregate(h *events.Header, voteSet []wire.Event) (*events.AggregatedAgreement, error) {
	stepVotesMap := make(map[uint8]struct {
		*events.StepVotes
		sortedset.Set
	})

	for _, ev := range voteSet {
		reduction := ev.(*events.Reduction)
		sv, found := stepVotesMap[reduction.Step]
		if !found {
			sv.StepVotes = events.NewStepVotes()
			sv.Set = sortedset.New()
		}

		if err := sv.StepVotes.Add(reduction); err != nil {
			return nil, err
		}
		sv.Set.Insert(reduction.PubKeyBLS)
		stepVotesMap[reduction.Step] = sv
	}

	aggregatedAgreement := events.NewAggregatedAgreement()
	aggregatedAgreement.Header = h
	i := 0
	for _, stepVotes := range stepVotesMap {
		sv, provisioners := stepVotes.StepVotes, stepVotes.Set
		sv.BitSet = r.ctx.committee.Pack(provisioners, h.Round, sv.Step)
		aggregatedAgreement.VotesPerStep[i] = sv
		i++
	}

	return aggregatedAgreement, nil
}
