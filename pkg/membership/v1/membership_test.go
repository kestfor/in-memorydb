package v1

import (
	"in-memorydb/pkg/membership/testsuite"
	"in-memorydb/pkg/types"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const testNodeName = "testnode"
const testNodeAddr = "127.9.9.9"
const testNodePort = uint16(55555)

type V1Suite struct {
	mainNode types.Node
	testsuite.MembershipSuite
}

func (s *V1Suite) SetupSuite() {
	mem, err := New(&Config{
		NodeName:       testNodeName,
		BindAddr:       testNodeAddr,
		MembershipPort: testNodePort,
	})
	s.Require().NoError(err)
	s.MembershipSuite.Mem = mem
	s.mainNode = s.MembershipSuite.Mem.LocalNode()
}

func (s *V1Suite) TearDownSuite() {
	err := s.MembershipSuite.Mem.Leave(time.Second)
	s.Require().NoError(err)
}

func (s *V1Suite) TestJoinMembersLeave() {
	newM, err := New(&Config{
		NodeName:       "testnode2",
		BindAddr:       "127.9.9.10",
		MembershipPort: testNodePort,
	})

	// joining
	s.Require().NoError(err)
	s.Require().NotNil(newM)
	err = newM.Join([]string{s.mainNode.MembershipAddr().String()})
	s.Require().NoError(err)

	// 2 members now
	members := s.Mem.Members()
	s.Require().Len(members, 2)

	// leaving
	err = newM.Leave(time.Second * 3)
	s.Require().NoError(err)

	// 1 member now
	members = s.Mem.Members()
	s.Require().Len(members, 1)

}

func (s *V1Suite) TestMeta() {
	m := meta{ExternalPort: 123, GossipPort: 456}
	bytes := m.toBytes()
	s.Require().NotNil(bytes)
	from := metaFromBytes(bytes)
	s.Require().NotNil(from)
	s.Require().Equal(from, m)
}

func TestV1MembershipSuite(t *testing.T) {
	suite.Run(t, &V1Suite{})
}
