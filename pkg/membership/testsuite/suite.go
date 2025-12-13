package testsuite

import (
	"github.com/kestfor/in-memorydb/pkg/membership"

	"github.com/stretchr/testify/suite"
)

// Factory создаёт конкретную реализацию Membership

// MembershipSuite — общий тестовый набор
type MembershipSuite struct {
	suite.Suite
	Mem membership.Membership
}

func (s *MembershipSuite) TestLocalNode() {
	node := s.Mem.LocalNode()

	s.NotEmpty(node.ID(), "ID should not be empty")
	s.NotNil(node.MembershipAddr(), "Address should not be nil")
}

func (s *MembershipSuite) TestMembersContainsLocal() {
	members := s.Mem.Members()
	s.NotEmpty(members, "Members should not be empty")

	localName := s.Mem.LocalNode().ID()
	found := false
	for _, n := range members {
		if n.ID() == localName {
			found = true
			break
		}
	}
	s.True(found, "Members() must include LocalNode")
}
