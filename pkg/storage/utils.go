package storage

import membershipv1 "github.com/kestfor/in-memorydb/pkg/membership/v1"

func GlobalCfg2Mem(config *Config) *membershipv1.Config {
	return &membershipv1.Config{
		AdvertiseAddr:  config.Membership.AdvertiseAddr,
		NodeName:       config.Node.ID,
		BindAddr:       config.Node.BindAddress,
		ExternalPort:   config.Node.Port,
		GossipPort:     config.Gossip.Port,
		MembershipPort: config.Membership.Port,
	}
}
