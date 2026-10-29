package core

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestAvailabilityUsesAllProcessesAndConservativeThresholds(t *testing.T) {
	now := time.Now()
	h := Demo(now)[0]
	h.Snapshot.GPUs[2].Util = Number(5)
	h.Snapshot.GPUs[2].Used = Number(512)
	h.Snapshot.GPUs[3].Used = Number(513)
	h.Snapshot.GPUs[5].Util = Number(6)
	h.Snapshot.GPUs[6].Used = nil
	h.Snapshot.GPUs[7].Util = Number(math.NaN())
	// 仍有计算进程时，即使指标均为零也不能视为空卡。
	h.Snapshot.GPUs[0].Util = Number(0)
	h.Snapshot.GPUs[0].Used = Number(0)
	a := GPUAvailability(h, now)
	if !a.Known || !reflect.DeepEqual(a.Free, []int{2}) || !reflect.DeepEqual(a.Busy, []int{0, 1, 3, 4, 5}) || !reflect.DeepEqual(a.Unknown, []int{6, 7}) {
		t.Fatalf("wrong availability: %+v", a)
	}
}
func TestUnavailableObservationsNeverCountAsFree(t *testing.T) {
	now := time.Now()
	for _, modify := range []func(*Host){func(h *Host) { h.Error = "offline" }, func(h *Host) { h.Snapshot.GPUOK = false }, func(h *Host) { h.Snapshot.ProcessesOK = false }, func(h *Host) { h.ReceivedAt = now.Add(-21 * time.Second) }, func(h *Host) { h.ReceivedAt = now.Add(time.Minute) }} {
		h := Demo(now)[0]
		modify(&h)
		a := GPUAvailability(h, now)
		if a.Known || len(a.Free) != 0 || len(a.Unknown) != 8 {
			t.Fatalf("failed observation treated as free: %+v", a)
		}
	}
}
