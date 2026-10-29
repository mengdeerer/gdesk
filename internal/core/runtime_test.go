package core

import (
	"testing"
	"time"
)

func TestRuntimeSourcesAndStaleness(t *testing.T) {
	now := time.Now()
	h := Demo(now)[0]
	r := Row{Processes: []Process{{ElapsedSeconds: Number(7200)}}}
	if got := RuntimeText(r, h, now); got != "已运行 2 小时 0 分钟" {
		t.Fatal(got)
	}
	r.Stale = true
	if got := RuntimeText(r, h, now.Add(time.Hour)); got != "已运行 2 小时 0 分钟 · 上次观测" {
		t.Fatal(got)
	}
	r.Stale = false
	r.Status.StartedAt = h.Snapshot.ObservedAt.Add(-90 * time.Minute)
	if got := RuntimeText(r, h, now); got != "已运行 1 小时 30 分钟" {
		t.Fatal(got)
	}
	r.Status.State = "succeeded"
	r.Status.FinishedAt = r.Status.StartedAt.Add(30 * time.Minute)
	if got := RuntimeText(r, h, now.Add(time.Hour)); got != "运行耗时 30 分钟" {
		t.Fatal(got)
	}
	r.Status.FinishedAt = time.Time{}
	if got := RuntimeText(r, h, now); got != "" {
		t.Fatal("missing finish time must not keep counting: " + got)
	}
	if got := RuntimeText(Row{}, h, now); got != "" {
		t.Fatal("unknown runtime fabricated: " + got)
	}
	r.Status.State = "running"
	r.Status.StartedAt = now.Add(time.Hour)
	if got := RuntimeText(r, h, now); got != "" {
		t.Fatal("negative runtime displayed: " + got)
	}
}
