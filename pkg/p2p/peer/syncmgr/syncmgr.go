// The sync manager will use a modified verison of the initial block download in bitcoin
// Seen here: https://en.bitcoinwiki.org/wiki/Bitcoin_Core_0.11_(ch_5):_Initial_Block_Download
// MovingWindow is a desired featured from the original codebase

package syncmgr

import (
	"encoding/hex"
	log "github.com/sirupsen/logrus"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/core"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/peer/peermgr"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire/commands"
	"gitlab.dusk.network/dusk-core/dusk-go/pkg/p2p/wire/payload"
	"net"
)

var (
	// This is the maximum amount of inflight objects that we would like to have
	// Number taken from original codebase
	maxBlockRequest = 1024

	// This is the maximum amount of blocks that we will ask for from a single peer
	// Number taken from original codebase
	maxBlockRequestPerPeer = 16
)

// Syncmgr holds pointers to peer- and address-manager and keeps the state of
// synchronisation of headers and blocks
type Syncmgr struct {
	pcfg              peermgr.ResponseHandler
	pmgr              *peermgr.PeerMgr
	Mode              int // 1 = headersFirst, 2 = Blocks, 3 = Maintain
	chain             *core.Blockchain
	headers           [][]byte
	inflightBlockReqs map[string]*peermgr.Peer // When we send a req for block, we will put hash in here, along with peer who we requested it from
}

// CreatePeer is called after a connection to a peer was successful.
func (s *Syncmgr) CreatePeer(con net.Conn, inbound bool) *peermgr.Peer {
	p := peermgr.NewPeer(con, inbound, s.pcfg)
	s.AddPeer(p)

	// TODO: set the unchangeable states
	return p
}

// AddPeer adds a peer for the peer manager to use
func (s *Syncmgr) AddPeer(peer *peermgr.Peer) {
	s.pmgr.AddPeer(peer)
}

// OnGetHeaders receives 'getheaders' msgs from a peer, reads them from the chain db
// and sends them to the requesting peer.
func (s *Syncmgr) OnGetHeaders(p *peermgr.Peer, msg *payload.MsgGetHeaders) {
	log.WithField("prefix", "syncmgr").Debug("Syncmgr OnGetHeaders called")
	// The caller peer wants some headers from our blockchain.
	msgHeaders, err := getHeaders(*s.chain, msg)
	if err == nil {
		p.Write(msgHeaders)
	} else {
		log.WithField("prefix", "syncmgr").Errorf("Failed to send '%s' to requesting peer %s: %s", commands.Headers, p.RemoteAddr().String(), err)
	}
}

// OnHeaders receives 'headers' msgs from an other peer and adds them to the chain.
func (s *Syncmgr) OnHeaders(p *peermgr.Peer, msg *payload.MsgHeaders) {
	log.WithField("prefix", "syncmgr").Debug("Sync manager OnHeaders called")

	// Any headers received?
	if len(msg.Headers) < 1 {
		log.WithField("prefix", "syncmgr").Infof("'%s' msg is empty", commands.Headers)
		return
	}

	// On receipt of Headers check what mode we are in
	// HeadersMode, we check if there is 2k. If so call again. If not then change mode into BlocksOnly
	if s.Mode == 1 {
		err := s.HeadersFirstMode(p, msg)
		if err != nil {
			log.WithField("prefix", "syncmgr").Error("Failed to read block headers:", err)
			return // TODO:We should custom name error so, that we can do something on WrongHash Error, Peer disconnect error
		}
		return
	}
}

// HeadersFirstMode receives 'headers' msgs from an other peer and adds them to the chain.
func (s *Syncmgr) HeadersFirstMode(p *peermgr.Peer, msg *payload.MsgHeaders) error {
	log.WithField("prefix", "syncmgr").Debug("Headers first mode")

	// Validate Headers
	err := s.chain.ValidateHeaders(msg)

	if err != nil {
		// Re-request headers from a different peer
		s.pmgr.Disconnect(p)
		log.WithField("prefix", "syncmgr").Error("Failed to validate headers:", err)
		return err
	}

	// Add Headers into db
	err = s.chain.AddHeaders(msg)
	if err != nil {
		// Try addding them into the db again?
		// Since this is simply a db insert, any problems here means trouble
		//TODO: Should we Switch off system or warn the user that the system is corrupted?
		log.WithField("prefix", "syncmgr").Error("Failed to add headers", err)

		//TODO: Batching is not yet implemented,
		// So here we would need to remove headers which have been added
		// from the slice
		return err
	}

	// Add header hashes into slice
	// Request first batch of blocks here
	hashes := make([][]byte, len(msg.Headers))
	for _, header := range msg.Headers {
		hashes = append(hashes, header.Hash)
	}
	s.headers = append(s.headers, hashes...)

	if len(msg.Headers) == 2*1e3 { // should be less than 2000, leave it as this for tests
		log.WithField("prefix", "syncmgr").Debug("Switching to BlocksOnly Mode")
		s.Mode = 2 // switch to BlocksOnly. XXX: because HeadersFirst is not in parallel, no race condition here.
		return s.RequestMoreBlocks()
	}
	latestHeader := msg.Headers[len(msg.Headers)-1]
	_, err = s.pmgr.RequestHeaders(latestHeader.Hash)
	return err
}

// RequestMoreBlocks request blocks from an other peer and keeps an admin of the requested blocks and peers.
func (s *Syncmgr) RequestMoreBlocks() error {
	var blockReq [][]byte
	var reqAmount int

	if len(s.headers) >= maxBlockRequestPerPeer {
		reqAmount = maxBlockRequestPerPeer
		blockReq = s.headers[:reqAmount]
	} else {
		reqAmount = len(s.headers)
		blockReq = s.headers[:reqAmount]
	}
	peer, err := s.pmgr.RequestBlocks(blockReq)
	if err != nil { // This could happen if the peermanager has no valid peers to connect to. We should wait a bit and re-request
		return err // alternatively we could make RequestBlocks blocking, then make sure it is not triggered when a block is received
	}

	//TODO: Possible race condition, between us requesting the block and adding it to
	// the inflight block map? Give that node a medal.

	for _, hash := range s.headers {
		hashKey := hex.EncodeToString(hash)
		s.inflightBlockReqs[hashKey] = peer
	}
	s.headers = s.headers[reqAmount:]
	// NONONO: Here we do not pass all of the hashes to peermanager because
	// it is not the peermanagers responsibility to mange inflight blocks
	return err
}

// RequestAddresses request addresses from an other peer
func (s *Syncmgr) RequestAddresses() error {
	return s.pmgr.RequestAddresses()
}

// OnGetData receives 'getdata' msgs from a peer.
// This could be a request for a specifx Tx or Block and will be read from the chain db.
// and send to the requesting peer.
func (s *Syncmgr) OnGetData(p *peermgr.Peer, msg *payload.MsgGetData) {
	// TODO
}

// OnBlock receives a block from a peer, then passes it to the blockchain to process.
// For now we will only use this simple setup, to allow us to test the other parts of the system.
// See Issue #24
func (s *Syncmgr) OnBlock(p *peermgr.Peer, msg *payload.MsgBlock) {
	//TODO
	//err := s.chain.AcceptBlock() //AddBlock(msg)
	//if err != nil {
	//	// Put headers back in front of queue to fetch block for.
	//	log.WithField("prefix", "syncmgr").Error("Block had an error", err)
	//}
}

// OnMemPool (TODO)
func (s *Syncmgr) OnMemPool(p *peermgr.Peer, msg *payload.MsgMemPool) {
	//err := s.chain.AddMempool(msg)
	//if err != nil {
	//	// Put headers back in front of queue to fetch block for.
	//	fmt.Println("Block had an error", err)
	//}
}