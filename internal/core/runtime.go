package core

import (
	"fmt"
	"math"
	"time"
)

// StartAge 使用远端同一时钟计算开始时间距观测的间隔，供最近启动排序使用。
// 结束任务仍按启动时间排序，不把短暂的历史任务误排到最前。
func StartAge(r Row, h Host) (float64, bool) {
	if !r.Status.StartedAt.IsZero() && !h.Snapshot.ObservedAt.IsZero() {
		age := h.Snapshot.ObservedAt.Sub(r.Status.StartedAt).Seconds()
		if age >= 0 {
			return age, true
		}
	}
	age := -1.0
	for _, p := range r.Processes {
		if p.ElapsedSeconds != nil && *p.ElapsedSeconds >= 0 && !math.IsNaN(*p.ElapsedSeconds) && !math.IsInf(*p.ElapsedSeconds, 0) && *p.ElapsedSeconds > age {
			age = *p.ElapsedSeconds
		}
	}
	return age, age >= 0
}

// RuntimeText 优先使用任务起止时间；自动发现的任务使用远端进程存活时长。
// 缺少可靠来源时留空，过期观测不继续计时。
func RuntimeText(r Row, h Host, now time.Time) string {
	terminal := r.Status.State == "succeeded" || r.Status.State == "failed" || r.Status.State == "cancelled"
	seconds := -1.0
	label := "已运行 "
	if terminal {
		if !r.Status.StartedAt.IsZero() && !r.Status.FinishedAt.IsZero() {
			seconds = r.Status.FinishedAt.Sub(r.Status.StartedAt).Seconds()
		}
		label = "运行耗时 "
	} else if len(r.Processes) > 0 {
		if !r.Status.StartedAt.IsZero() && !h.Snapshot.ObservedAt.IsZero() {
			seconds = h.Snapshot.ObservedAt.Sub(r.Status.StartedAt).Seconds()
		} else {
			for _, p := range r.Processes {
				if p.ElapsedSeconds != nil && !math.IsInf(*p.ElapsedSeconds, 0) && !math.IsNaN(*p.ElapsedSeconds) && *p.ElapsedSeconds > seconds {
					seconds = *p.ElapsedSeconds
				}
			}
		}
		if seconds >= 0 && !r.Stale && FreshHost(h, now) {
			seconds += math.Max(0, now.Sub(h.ReceivedAt).Seconds())
		}
	}
	if seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return ""
	}
	minutes := int(seconds / 60)
	duration := "不足 1 分钟"
	if minutes >= 1440 {
		duration = fmt.Sprintf("%d 天 %d 小时", minutes/1440, minutes/60%24)
	} else if minutes >= 60 {
		duration = fmt.Sprintf("%d 小时 %d 分钟", minutes/60, minutes%60)
	} else if minutes > 0 {
		duration = fmt.Sprintf("%d 分钟", minutes)
	}
	if !terminal && (r.Stale || !FreshHost(h, now)) {
		duration += " · 上次观测"
	}
	return label + duration
}
