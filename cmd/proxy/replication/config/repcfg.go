package repconfig

import (
	"fmt"
	"strings"
	"time"

	"juno/pkg/io"
	"juno/pkg/util"
)

var (
	kDefaultName = "default"

	kDefaultReplicationIoConfig = io.OutboundConfig{
		ConnectTimeout:        util.Duration{1 * time.Second},
		ReqChanBufSize:        8092,
		MaxPendingQueSize:     8092,
        PendingQueExtra:       300,
		MaxBufferedWriteSize:  64 * 1024, // default 64k
		ReconnectIntervalBase: 100,       // 100ms
		ReconnectIntervalMax:  20000,     // 20 seconds
		NumConnsPerTarget:     1,
		IOBufSize:             64 * 1024,
		ConnectRecycleT:       util.Duration{180 * time.Second},
		EnableConnRecycle:     true,
		GracefulShutdownTime:  util.Duration{2 * time.Second},
	}
	DefaultConfig = Config{
		IO: io.OutboundConfigMap{kDefaultName: kDefaultReplicationIoConfig},
	}
)

type (
	ReplicationTarget struct {
		Name string
		io.ServiceEndpoint
		UseMayflyProtocol bool
		Namespaces        []string
		BypassLTMEnabled  bool
	}

	Config struct {
		Targets []ReplicationTarget
		IO      io.OutboundConfigMap
	}
)

func (c *Config) GetIoConfig(target *ReplicationTarget) *io.OutboundConfig {
	if target != nil {

		if cfg, ok := c.IO[target.Name]; ok {
			return &cfg
		} else {
			if cfg, ok = c.IO[kDefaultName]; ok {
				return &cfg
			}
		}
	}
	return &kDefaultReplicationIoConfig
}

func (c *Config) Validate() {

	for i := len(c.Targets) - 1; i >= 0; i-- {
		t := &c.Targets[i]
		if strings.TrimSpace(t.Addr) == "" {
			c.Targets = append(c.Targets[:i], c.Targets[i+1:]...)
		} else {
			if len(t.Name) == 0 {
				c.Targets[i].Name = fmt.Sprintf("t%d", i)
			}
			if len(t.Network) == 0 {
				c.Targets[i].Network = "tcp"
			}
		}
	}
	c.IO.SetDefaultIfNotDefined()
}