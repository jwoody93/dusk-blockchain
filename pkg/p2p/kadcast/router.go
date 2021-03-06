package kadcast

import (
	"net"
	"sync"
	"sort"
	"time"
)

// K is the number of peers that a node will send on
// a `NODES` message.
const K int = 20

// Alpha is the number of nodes to which a node will
// ask for new nodes with `FIND_NODES` messages.
const Alpha int = 3

// Router holds all of the data needed to interact with
// the routing data and also the networking utils.
type Router struct {
	// Tree represents the routing structure.
	tree Tree
	// Even the port and the IP are the same info, the difference
	// is that one IP has type `IP` and the other `[4]byte`.
	// Since we only store one tree on the application, it's worth
	// to keep both in order to avoid convert the types continuously.
	myPeerUDPAddr net.UDPAddr
	MyPeerInfo    Peer
	// Holds the Nonce that satisfies: `H(ID || Nonce) < Tdiff`.
	myPeerNonce uint32
}

// MakeRouter allows to create a router which holds the peerInfo and
// also the routing tree information.
func MakeRouter(externIP [4]byte, port uint16) Router {
	myPeer := MakePeer(externIP, port)
	return Router{
		tree:          makeTree(myPeer),
		myPeerUDPAddr: myPeer.getUDPAddr(),
		MyPeerInfo:    myPeer,
		myPeerNonce:   myPeer.computePeerNonce(),
	}
}

// --------------------------------------------------//
//													 //
// Tools to get sorted Peers in respect to a certain //
// PeerID in terms of XOR-distance.				     //
//													 //
// --------------------------------------------------//

// Returns the complete list of Peers in order to be sorted
// as they have the xor distance in respec to a Peer as a parameter.
func (router Router) getPeerSortDist(refPeer Peer) []PeerSort {
	var peerList []Peer
	for buckIdx, bucket := range router.tree.buckets {
		// Skip bucket 0
		if buckIdx != 0 {
			peerList = append(peerList[:], bucket.entries[:]...)
		}
	}
	var peerListSort []PeerSort
	for _, peer := range peerList {
		// We don't want to return the Peer struct of the Peer
		// that is the reference.
		if peer != refPeer {
			peerListSort = append(peerListSort[:],
				PeerSort{
					ip:        peer.ip,
					port:      peer.port,
					id:        peer.id,
					xorMyPeer: xor(refPeer.id, peer.id),
				})
		}
	}
	return peerListSort
}

// ByXORDist implements sort.Interface based on the IdDistance
// respective to myPeerId.
type ByXORDist []PeerSort

func (a ByXORDist) Len() int { return len(a) }
func (a ByXORDist) Less(i int, j int) bool {
	return !xorIsBigger(a[i].xorMyPeer, a[j].xorMyPeer)
}
func (a ByXORDist) Swap(i, j int) { a[i], a[j] = a[j], a[i] }

// Returns a list of the selected number of closest peers
// in respect to a certain `Peer`.
func (router Router) getXClosestPeersTo(peerNum int, refPeer Peer) []Peer {
	var xPeers []Peer
	peerList := router.getPeerSortDist(refPeer)
	sort.Sort(ByXORDist(peerList))

	// Get the `peerNum` closest ones.
	for _, peer := range peerList {
		xPeers = append(xPeers[:],
			Peer{
				ip:   peer.ip,
				port: peer.port,
				id:   peer.id,
			})
		if len(xPeers) >= peerNum {
			break
		}
	}
	return xPeers
}

// Sends a `FIND_NODES` messages to the `alpha` closest peers
// the node knows and waits for a certain time in order to wait 
// for the `PONG` message arrivals.
// Then looks for the closest peer to the node itself into the
// buckets and returns it.
func (router Router) pollClosestPeer(t time.Duration) Peer {
	var wg sync.WaitGroup
	var ps []Peer
	wg.Add(1) 
	router.sendFindNodes()

	timer := time.AfterFunc(t, func() {
		ps = router.getXClosestPeersTo(1, router.MyPeerInfo)
		wg.Done()
	})

	wg.Wait()
	timer.Stop()
	return ps[0]
}

// Sends a `PING` messages to the bootstrap nodes that
// the node knows and waits for a certain time in order to wait 
// for the `PONG` message arrivals.
// Returns back the new number of peers the node is connected to.
func (router Router) pollBootstrappingNodes(bootNodes []Peer, t time.Duration) uint64 {
	var wg sync.WaitGroup
	var peerNum uint64

	wg.Add(1) 
	for _, peer := range bootNodes {
		router.sendPing(peer)
	}

	timer := time.AfterFunc(t, func() {
		peerNum = uint64(router.tree.getTotalPeers())
		wg.Done()
	})

	wg.Wait()
	timer.Stop()
	return peerNum
}

// ------- Packet-sending utilities for the Router ------- //

// Builds and sends a `PING` packet
func (router Router) sendPing(receiver Peer) {
	// Build empty packet.
	var packet Packet
	// Fill the headers with the type, ID, Nonce and destPort.
	packet.setHeadersInfo(0, router, receiver)

	// Since return values from functions are not addressable, we need to
	// allocate the receiver UDPAddr
	destUDPAddr := receiver.getUDPAddr()
	// Send the packet
	sendUDPPacket("udp", destUDPAddr, packet.asBytes())
}

// Builds and sends a `PONG` packet
func (router Router) sendPong(receiver Peer) {
	// Build empty packet.
	var packet Packet
	// Fill the headers with the type, ID, Nonce and destPort.
	packet.setHeadersInfo(1, router, receiver)

	// Since return values from functions are not addressable, we need to
	// allocate the receiver UDPAddr
	destUDPAddr := receiver.getUDPAddr()
	// Send the packet
	sendUDPPacket("udp", destUDPAddr, packet.asBytes())
}

// Builds and sends a `FIND_NODES` packet.
func (router Router) sendFindNodes() {
	// Get `Alpha` closest nodes to me.
	destPeers := router.getXClosestPeersTo(Alpha, router.MyPeerInfo)
	// Fill the headers with the type, ID, Nonce and destPort.
	for _, peer := range destPeers {
		// Build the packet
		var packet Packet
		packet.setHeadersInfo(2, router, peer)
		// We don't need to add the ID to the payload snce we already have
		// it in the headers.
		// Send the packet
		sendUDPPacket("udp", peer.getUDPAddr(), packet.asBytes())
	}
}

// Builds and sends a `NODES` packet.
func (router Router) sendNodes(receiver Peer) {
	// Build empty packet
	var packet Packet
	// Set headers
	packet.setHeadersInfo(3, router, receiver)
	// Set payload with the `k` peers closest to receiver.
	peersToSend := packet.setNodesPayload(router, receiver)
	// If we don't have any peers to announce, we just skip sending
	// the `NODES` messsage.
	if peersToSend == 0 {
		return
	}
	sendUDPPacket("udp", receiver.getUDPAddr(), packet.asBytes())
}
