package factory

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"time"

	"github.com/bwesterb/go-ristretto"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/committee"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/generation"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/msg"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/notary"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/reduction"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/selection"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/user"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core/consensus/voting"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire"
)

type initCollector struct {
	initChannel chan uint64
}

func (i *initCollector) Collect(roundBuffer *bytes.Buffer) error {
	round := binary.LittleEndian.Uint64(roundBuffer.Bytes())
	i.initChannel <- round
	return nil
}

// ConsensusFactory is responsible for initializing the consensus processes
// with the proper parameters. It subscribes to the initialization topic and,
// upon reception of a message, will start all of the components related to
// consensus. It should also contain all the relevant information for the
// processes it intends to start up.
type ConsensusFactory struct {
	eventBus    *wire.EventBus
	initChannel chan uint64

	*user.Keys
	timerLength time.Duration
	committee   committee.Committee
	d, k        ristretto.Scalar
}

// New returns an initialized ConsensusFactory.
func New(eventBus *wire.EventBus, timerLength time.Duration,
	committee committee.Committee, keys *user.Keys, d, k ristretto.Scalar) *ConsensusFactory {
	initChannel := make(chan uint64, 1)

	initCollector := &initCollector{initChannel}
	go wire.NewEventSubscriber(eventBus, initCollector, msg.InitializationTopic).Accept()

	return &ConsensusFactory{
		eventBus:    eventBus,
		initChannel: initChannel,
		Keys:        keys,
		timerLength: timerLength,
		committee:   committee,
		d:           d,
		k:           k,
	}
}

// StartConsensus will wait for a message to come in, and then proceed to
// start the consensus components.
func (c *ConsensusFactory) StartConsensus() {
	fmt.Printf("Starting consensus")
	round := <-c.initChannel
	fmt.Printf("Initing on round %d\n", round)

	generation.LaunchGeneratorComponent(c.eventBus, c.d, c.k)
	voting.LaunchVotingComponent(c.eventBus, c.Keys, c.committee)

	selection.LaunchScoreSelectionComponent(c.eventBus, c.timerLength)
	selection.LaunchSignatureSelector(c.committee, c.eventBus, c.timerLength)

	reduction.LaunchBlockReducer(c.eventBus, c.committee, c.timerLength)
	reduction.LaunchSigSetReducer(c.eventBus, c.committee, c.timerLength)

	notary.LaunchBlockNotary(c.eventBus, c.committee)
	notary.LaunchSignatureSetNotary(c.eventBus, c.committee, round)

	fmt.Println("consensus started")
}