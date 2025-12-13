package membership

import (
	"github/kestfor/in-memorydb/pkg/types"
	"time"
)

type Membership interface {

	// LocalNode returns the Node instance representing the current node in the cluster.
	LocalNode() types.Node

	// Members return a slice of Node instances representing all the current members of the cluster.
	Members() []types.Node

	// Leave gracefully removes the current node from the cluster with a specified timeout indicating the leave duration.
	Leave(timeout time.Duration) error

	// Join allows the current node to join a cluster using the provided list of seed node addresses.
	Join(seeds []string) error
}
