// Package sysinfo samples process CPU usage and process/host memory. It reads
// the Linux proc filesystem (the deployment target is Linux containers) and
// degrades to zero values on other platforms.
package sysinfo

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Stats is a point-in-time resource sample.
type Stats struct {
	// CPUPercent is process CPU usage since the previous sample, where 100
	// means one full core.
	CPUPercent float64 `json:"cpu_percent"`
	// RSSBytes is the process resident set size.
	RSSBytes int64 `json:"rss_bytes"`
	// MemTotalBytes and MemAvailableBytes describe the host (or container
	// namespace) memory.
	MemTotalBytes     int64 `json:"mem_total_bytes"`
	MemAvailableBytes int64 `json:"mem_available_bytes"`
}

// Sampler computes CPU deltas between successive Sample calls.
type Sampler struct {
	mu        sync.Mutex
	lastTicks uint64
	lastAt    time.Time
	clockTick float64
}

func NewSampler() *Sampler {
	return &Sampler{clockTick: 100} // Linux USER_HZ is 100 on all supported targets.
}

func (s *Sampler) Sample() Stats {
	stats := Stats{}
	stats.RSSBytes, stats.MemTotalBytes, stats.MemAvailableBytes = memoryStats()
	ticks, ok := processCPUTicks()
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok && !s.lastAt.IsZero() && ticks >= s.lastTicks {
		elapsed := now.Sub(s.lastAt).Seconds()
		if elapsed > 0 {
			stats.CPUPercent = float64(ticks-s.lastTicks) / s.clockTick / elapsed * 100
		}
	}
	if ok {
		s.lastTicks = ticks
		s.lastAt = now
	}
	return stats
}

func processCPUTicks() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0, false
	}
	// The command field may contain spaces; skip past the closing parenthesis.
	text := string(data)
	closing := strings.LastIndexByte(text, ')')
	if closing < 0 {
		return 0, false
	}
	fields := strings.Fields(text[closing+1:])
	// After the command field: state is index 0, utime is 11, stime is 12.
	if len(fields) < 13 {
		return 0, false
	}
	utime, err1 := strconv.ParseUint(fields[11], 10, 64)
	stime, err2 := strconv.ParseUint(fields[12], 10, 64)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	return utime + stime, true
}

func memoryStats() (rss, total, available int64) {
	if file, err := os.Open("/proc/self/status"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			if value, ok := strings.CutPrefix(scanner.Text(), "VmRSS:"); ok {
				rss = parseKB(value)
				break
			}
		}
		_ = file.Close()
	}
	if file, err := os.Open("/proc/meminfo"); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if value, ok := strings.CutPrefix(line, "MemTotal:"); ok {
				total = parseKB(value)
			} else if value, ok := strings.CutPrefix(line, "MemAvailable:"); ok {
				available = parseKB(value)
			}
			if total > 0 && available > 0 {
				break
			}
		}
		_ = file.Close()
	}
	return rss, total, available
}

func parseKB(value string) int64 {
	fields := strings.Fields(value)
	if len(fields) < 1 {
		return 0
	}
	parsed, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return parsed * 1024
}
