package types

import "net"

type Node interface {

	// ID returns the unique identifier of the node as a string.
	ID() string

	// GossipAddr returns the network address used by the node for gossip communication.
	GossipAddr() net.Addr

	// MembershipAddr returns the network address used by the node for membership communication.
	MembershipAddr() net.Addr

	// ExternalAddr returns the external network address of the node used for client communication or external services.
	ExternalAddr() net.Addr
}
