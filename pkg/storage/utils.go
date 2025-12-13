package storage

import membershipv1 "github/kestfor/in-memorydb/pkg/membership/v1"

func globalCfg2Mem(config *Config) *membershipv1.Config {
	return &membershipv1.Config{
		NodeName:       config.Node.ID,
		BindAddr:       config.Node.BindAddress,
		ExternalPort:   config.Node.Port,
		GossipPort:     config.Gossip.Port,
		MembershipPort: config.Membership.Port,
	}
}
