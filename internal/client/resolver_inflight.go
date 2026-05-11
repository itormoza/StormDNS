package client

import "errors"

const defaultSpeculativePipelineWindow = 16

var ErrResolverInflightWindowFull = errors.New("resolver in-flight query window full")

func resolverInflightKeyForConnection(conn Connection) string {
	if conn.Resolver != "" && conn.ResolverPort > 0 {
		return formatResolverEndpoint(conn.Resolver, conn.ResolverPort)
	}
	if conn.ResolverLabel != "" {
		return conn.ResolverLabel
	}
	return conn.Key
}

func (c *Client) speculativePipelineWindow() int {
	if c == nil {
		return defaultSpeculativePipelineWindow
	}
	if c.cfg.SpeculativePipelineWindow > 0 {
		return c.cfg.SpeculativePipelineWindow
	}
	return defaultSpeculativePipelineWindow
}

func (c *Client) tryReserveResolverInflight(inflightKey string) bool {
	if c == nil || inflightKey == "" {
		return false
	}

	window := c.speculativePipelineWindow()
	if window < 1 {
		window = 1
	}

	c.resolverStatsMu.Lock()
	defer c.resolverStatsMu.Unlock()

	if c.resolverInflight == nil {
		c.resolverInflight = make(map[string]int)
	}
	if c.resolverInflight[inflightKey] >= window {
		return false
	}
	c.resolverInflight[inflightKey]++
	return true
}

func (c *Client) resolverInflightCount(inflightKey string) int {
	if c == nil || inflightKey == "" {
		return 0
	}
	c.resolverStatsMu.RLock()
	defer c.resolverStatsMu.RUnlock()
	return c.resolverInflight[inflightKey]
}

func (c *Client) releaseResolverInflight(inflightKey string) {
	if c == nil || inflightKey == "" {
		return
	}
	c.resolverStatsMu.Lock()
	released := c.releaseResolverInflightLocked(inflightKey)
	c.resolverStatsMu.Unlock()
	if released {
		c.signalResolverInflightSpace()
	}
}

func (c *Client) releaseResolverInflightKeys(inflightKeys []string) {
	if c == nil || len(inflightKeys) == 0 {
		return
	}

	c.resolverStatsMu.Lock()
	released := false
	for _, key := range inflightKeys {
		if c.releaseResolverInflightLocked(key) {
			released = true
		}
	}
	c.resolverStatsMu.Unlock()

	if released {
		c.signalResolverInflightSpace()
	}
}

func (c *Client) releaseResolverInflightLocked(inflightKey string) bool {
	if c == nil || inflightKey == "" || c.resolverInflight == nil {
		return false
	}

	count := c.resolverInflight[inflightKey]
	if count <= 0 {
		return false
	}
	if count == 1 {
		delete(c.resolverInflight, inflightKey)
		return true
	}
	c.resolverInflight[inflightKey] = count - 1
	return true
}

func (c *Client) releaseResolverSampleInflightLocked(sample resolverSample) bool {
	if sample.inflightKey == "" || sample.inflightReleased {
		return false
	}
	return c.releaseResolverInflightLocked(sample.inflightKey)
}

func (c *Client) signalResolverInflightSpace() {
	if c == nil || c.txWindowSignal == nil {
		return
	}
	select {
	case c.txWindowSignal <- struct{}{}:
	default:
	}
}

func (c *Client) clearResolverInflightSignal() {
	if c == nil || c.txWindowSignal == nil {
		return
	}
	for {
		select {
		case <-c.txWindowSignal:
		default:
			return
		}
	}
}
