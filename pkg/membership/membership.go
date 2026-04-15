package membership

import (
	"time"

	"github.com/kestfor/in-memorydb/pkg/types"
)

type MemberSnapshot struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	IsLocal        bool   `json:"is_local"`
	MembershipAddr string `json:"membership_addr"`
	GossipAddr     string `json:"gossip_addr"`
	ExternalAddr   string `json:"external_addr"`
}

type Membership interface {
	LocalNode() types.Node
	Members() []types.Node
	Leave(timeout time.Duration) error
	Join(seeds []string) error
	Num() int
	Snapshot() []MemberSnapshot
}
