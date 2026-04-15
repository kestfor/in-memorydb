package v1

import (
	"encoding/binary"
	"log/slog"
	"strconv"
	"time"

	"github.com/kestfor/in-memorydb/pkg/membership"
	"github.com/kestfor/in-memorydb/pkg/types"
	"github.com/kestfor/in-memorydb/pkg/utils/logging"

	"github.com/hashicorp/memberlist"
)

type delegate struct {
	meta meta
}

func (d *delegate) NodeMeta(limit int) []byte {
	return d.meta.toBytes()
}

func (d *delegate) NotifyMsg([]byte)                           {}
func (d *delegate) GetBroadcasts(overhead, limit int) [][]byte { return nil }
func (d *delegate) LocalState(join bool) []byte                { return nil }
func (d *delegate) MergeRemoteState(buf []byte, join bool)     {}

type meta struct {
	GossipPort   uint16
	ExternalPort uint16
}

func (m meta) toBytes() []byte {
	return []byte{byte(m.GossipPort >> 8), byte(m.GossipPort), byte(m.ExternalPort >> 8), byte(m.ExternalPort)}
}

func metaFromBytes(b []byte) meta {
	var m meta
	m = meta{
		GossipPort:   binary.BigEndian.Uint16(b[:2]),
		ExternalPort: binary.BigEndian.Uint16(b[2:4]),
	}
	return m
}

type Config struct {
	NodeName       string
	AdvertiseAddr  string
	BindAddr       string
	GossipPort     uint16
	MembershipPort uint16

	// ExternalPort is a port for clients
	ExternalPort uint16
}

type memImpl struct {
	list *memberlist.Memberlist
}

func New(cfg *Config) (membership.Membership, error) {
	memConfig := memberlist.DefaultWANConfig()
	stdLogger := slog.NewLogLogger(slog.Default().Handler(), logging.LogLevel())
	memConfig.Logger = stdLogger
	memConfig.Name = cfg.NodeName

	memConfig.BindAddr = cfg.BindAddr
	memConfig.BindPort = int(cfg.MembershipPort)

	memConfig.AdvertiseAddr = cfg.AdvertiseAddr
	memConfig.AdvertisePort = memConfig.BindPort

	memConfig.Delegate = &delegate{
		meta: meta{GossipPort: cfg.GossipPort, ExternalPort: cfg.ExternalPort},
	}

	memList, err := memberlist.Create(memConfig)
	if err != nil {
		return nil, err
	}

	return &memImpl{
		list: memList,
	}, nil
}

func (m *memImpl) Join(seeds []string) error {
	_, err := m.list.Join(seeds)
	return err
}

func (m *memImpl) Num() int {
	return m.list.NumMembers()
}

func (m *memImpl) LocalNode() types.Node {
	return &node{m.list.LocalNode()}

}

func (m *memImpl) Leave(timeout time.Duration) error {
	return m.list.Leave(timeout)
}

func (m *memImpl) Members() []types.Node {
	nodes := m.list.Members()
	converted := make([]types.Node, len(nodes))
	for i, n := range nodes {
		converted[i] = &node{n}
	}
	return converted
}

func (m *memImpl) Snapshot() []membership.MemberSnapshot {
	nodes := m.list.Members()
	result := make([]membership.MemberSnapshot, 0, len(nodes))
	localID := m.list.LocalNode().Name

	for _, n := range nodes {
		wrapped := &node{n}
		result = append(result, membership.MemberSnapshot{
			ID:             wrapped.ID(),
			Status:         memberStatus(n.State),
			IsLocal:        wrapped.ID() == localID,
			MembershipAddr: wrapped.MembershipAddr().String(),
			GossipAddr:     wrapped.GossipAddr().String(),
			ExternalAddr:   wrapped.ExternalAddr().String(),
		})
	}

	return result
}

func memberStatus(state memberlist.NodeStateType) string {
	switch state {
	case memberlist.StateAlive:
		return "alive"
	case memberlist.StateSuspect:
		return "suspect"
	case memberlist.StateDead:
		return "dead"
	case memberlist.StateLeft:
		return "left"
	default:
		return "unknown:" + strconv.Itoa(int(state))
	}
}
