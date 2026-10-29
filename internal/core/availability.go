package core

import (
	"math"
	"sort"
	"time"
)

type Availability struct {
	Known   bool
	Free    []int
	Busy    []int
	Unknown []int
}

func FreshHost(h Host, now time.Time) bool {
	age := now.Sub(h.ReceivedAt)
	return h.Error == "" && h.Snapshot.GPUOK && h.Snapshot.ProcessesOK && !h.ReceivedAt.IsZero() && age >= 0 && age < 20*time.Second
}

// 空闲是瞬时观测候选：无计算进程、显存不超过 512 MiB、利用率不超过 5%。
// 始终基于完整主机观测，用户/搜索筛选不能把别人的任务变成空卡。
func GPUAvailability(h Host, now time.Time) Availability {
	result := Availability{Known: FreshHost(h, now)}
	used := map[string]bool{}
	for _, p := range h.Snapshot.Processes {
		for _, uuid := range p.GPUs {
			used[uuid] = true
		}
	}
	number := func(n *float64) bool { return n != nil && !math.IsNaN(*n) && !math.IsInf(*n, 0) && *n >= 0 }
	for _, g := range h.Snapshot.GPUs {
		switch {
		case !result.Known:
			result.Unknown = append(result.Unknown, g.Index)
		case used[g.UUID]:
			result.Busy = append(result.Busy, g.Index)
		case !number(g.Util) || *g.Util > 100 || !number(g.Used) || !number(g.Total) || *g.Total <= 0 || *g.Used > *g.Total:
			result.Unknown = append(result.Unknown, g.Index)
		case *g.Util <= 5 && *g.Used <= 512:
			result.Free = append(result.Free, g.Index)
		default:
			result.Busy = append(result.Busy, g.Index)
		}
	}
	sort.Ints(result.Free)
	sort.Ints(result.Busy)
	sort.Ints(result.Unknown)
	return result
}
