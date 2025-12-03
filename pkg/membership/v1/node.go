package v1

import (
	"net"
	"strconv"

	"github.com/hashicorp/memberlist"
)

type node struct {
	*memberlist.Node
}

type addr struct {
	ipAddr string
}

func (a *addr) Network() string {
	return "tcp"
}

func (a *addr) String() string {
	return a.ipAddr
}

func (n *node) ID() string {
	return n.Node.Name
}

func (n *node) MembershipAddr() net.Addr {
	return &addr{ipAddr: n.Node.Addr.String() + ":" + strconv.Itoa(int(n.Node.Port))}
}

func (n *node) ExternalAddr() net.Addr {
	metaRaw := n.Node.Meta
	m := metaFromBytes(metaRaw)
	return &addr{ipAddr: n.Node.Addr.String() + ":" + strconv.Itoa(int(m.ExternalPort))}
}

func (n *node) GossipAddr() net.Addr {
	metaRaw := n.Node.Meta
	m := metaFromBytes(metaRaw)
	return &addr{ipAddr: n.Node.Addr.String() + ":" + strconv.Itoa(int(m.GossipPort))}
}
