package config

func (c *Config) Validate() error {
	if err := c.Node.Validate(); err != nil {
		return err
	}
	if err := c.Gossip.Validate(); err != nil {
		return err
	}
	if err := c.Persistence.Validate(); err != nil {
		return err
	}
	if err := c.Replication.Validate(); err != nil {
		return err
	}
	if err := c.Security.Validate(); err != nil {
		return err
	}
	return nil
}

func (c *SecurityConfig) Validate() error {

	if c.Enabled {
		if c.CaCert == "" {
			return ErrMissingCaCert
		}

		if c.CaKey == "" {
			return ErrMissingCaKey
		}

		if c.Cert == "" {
			return ErrMissingCert
		}

		if c.Key == "" {
			return ErrMissingKey
		}

	}

	return nil
}

func (c *NodeConfig) Validate() error {
	return nil
}

func (c *GossipConfig) Validate() error {

	if !knownProtocols.Contains(c.Protocol) {
		return ErrUnknownProtocol
	}
	return nil
}

func (c *PersistenceConfig) Validate() error {
	return nil
}

func (c *ReplicationConfig) Validate() error {
	return nil
}
