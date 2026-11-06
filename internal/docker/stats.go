package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/pkg/stdcopy"
)

// ContainerStats is a point-in-time resource sample of one managed container.
type ContainerStats struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	DomainID   int64   `json:"domain_id,omitempty"`
	State      string  `json:"state"`
	CPUPercent float64 `json:"cpu_percent"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	PIDs       uint64  `json:"pids"`
}

// Stats samples every managed container concurrently.
func (o *Orchestrator) Stats(ctx context.Context) ([]ContainerStats, error) {
	list, err := o.cli.ContainerList(ctx, container.ListOptions{All: true,
		Filters: filters.NewArgs(filters.Arg("label", LabelManaged+"=true"))})
	if err != nil {
		return nil, err
	}
	out := make([]ContainerStats, len(list))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i, c := range list {
		s := ContainerStats{ID: c.ID[:12], Kind: c.Labels[LabelKind], State: c.State}
		if len(c.Names) > 0 {
			s.Name = strings.TrimPrefix(c.Names[0], "/")
		}
		s.DomainID, _ = strconv.ParseInt(c.Labels[LabelDomainID], 10, 64)
		out[i] = s
		if c.State != "running" {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if err := o.sample(ctx, id, &out[i]); err != nil {
				o.log.Debug("stats sample failed", "container", id, "err", err)
			}
		}(i, c.ID)
	}
	wg.Wait()
	return out, nil
}

func (o *Orchestrator) sample(ctx context.Context, id string, s *ContainerStats) error {
	r, err := o.cli.ContainerStatsOneShot(ctx, id)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	var st container.StatsResponse
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		return err
	}
	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
	cpus := float64(st.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(st.CPUStats.CPUUsage.PercpuUsage))
	}
	if sysDelta > 0 && cpuDelta > 0 {
		s.CPUPercent = cpuDelta / sysDelta * cpus * 100
	}
	s.MemUsage = st.MemoryStats.Usage
	if cache, ok := st.MemoryStats.Stats["inactive_file"]; ok && cache < s.MemUsage {
		s.MemUsage -= cache
	}
	s.MemLimit = st.MemoryStats.Limit
	for _, n := range st.Networks {
		s.NetRx += n.RxBytes
		s.NetTx += n.TxBytes
	}
	s.PIDs = st.PidsStats.Current
	return nil
}

// FollowSiteLogs streams the runtime container's stdout/stderr (the site's
// error log) into w until ctx is cancelled.
func (o *Orchestrator) FollowSiteLogs(ctx context.Context, domainID int64, tail int, w io.Writer) error {
	c, err := o.findContainer(ctx, SiteContainerName(domainID))
	if err != nil {
		return err
	}
	if c == nil {
		return ErrNoContainer
	}
	rc, err := o.cli.ContainerLogs(ctx, c.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Tail: strconv.Itoa(tail),
	})
	if err != nil {
		return fmt.Errorf("container logs: %w", err)
	}
	defer rc.Close()
	if c.Config.Tty {
		_, err = io.Copy(w, rc)
	} else {
		_, err = stdcopy.StdCopy(w, w, rc)
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}
