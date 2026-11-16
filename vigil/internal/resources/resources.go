package resources

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"vigil/internal/config"
)

type Process struct {
	PID       int     `json:"pid"`
	Parent    int     `json:"parent_pid"`
	CPU       float64 `json:"cpu_percent"`
	RSSMiB    int     `json:"rss_mib"`
	Ownership string  `json:"ownership"`
}
type Snapshot struct {
	Processes    []Process `json:"agent_processes"`
	FreeDiskMiB  *uint64   `json:"free_disk_mib"`
	LongCommands int       `json:"observed_long_commands"`
	Warnings     []string  `json:"warnings"`
}

func Sample(ctx context.Context, dir string, longCommands int, cfg config.Resources) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	b, err := exec.CommandContext(ctx, "/bin/ps", "-axo", "pid=,ppid=,%cpu=,rss=,comm=").Output()
	if err != nil {
		return Snapshot{}, errors.New("process snapshot unavailable")
	}
	snapshot := analyze(string(b), os.Getpid(), longCommands, cfg)
	var stat syscall.Statfs_t
	if syscall.Statfs(dir, &stat) == nil {
		free := uint64(stat.Bavail) * uint64(stat.Bsize) / (1024 * 1024)
		snapshot.FreeDiskMiB = &free
		if free < uint64(cfg.MinFreeDiskMiB) {
			snapshot.Warnings = append(snapshot.Warnings, "low_free_disk")
		}
	}
	return snapshot, nil
}
func analyze(raw string, self, longCommands int, cfg config.Resources) Snapshot {
	s := Snapshot{Processes: []Process{}, LongCommands: longCommands, Warnings: []string{}}
	type entry struct {
		p     Process
		codex bool
	}
	entries := map[int]entry{}
	var order []int
	for _, line := range strings.Split(raw, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		pid, e1 := strconv.Atoi(fields[0])
		ppid, e2 := strconv.Atoi(fields[1])
		cpu, e3 := strconv.ParseFloat(fields[2], 64)
		rss, e4 := strconv.Atoi(fields[3])
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil {
			continue
		}
		name := filepath.Base(strings.Join(fields[4:], " "))
		entries[pid] = entry{Process{PID: pid, Parent: ppid, CPU: cpu, RSSMiB: rss / 1024}, name == "codex" || name == "codex-app-server"}
		order = append(order, pid)
	}
	cpuHigh, memoryHigh := false, false
	for _, pid := range order {
		e := entries[pid]
		if pid == self {
			continue
		}
		e.p.Ownership = "uncertain"
		related := e.codex
		for parent, n := e.p.Parent, 0; parent > 1 && n < 64; n++ {
			if parent == self {
				e.p.Ownership = "vigil_descendant"
				related = true
				break
			}
			ancestor, ok := entries[parent]
			if !ok {
				break
			}
			if ancestor.codex {
				related = true
				e.p.Ownership = "codex_descendant"
			}
			parent = ancestor.p.Parent
		}
		if !related {
			continue
		}
		cpuHigh = cpuHigh || e.p.CPU >= float64(cfg.MaxCPUPercent)
		memoryHigh = memoryHigh || e.p.RSSMiB >= cfg.MaxProcessMiB
		if len(s.Processes) < 128 {
			s.Processes = append(s.Processes, e.p)
		}
	}
	if cpuHigh {
		s.Warnings = append(s.Warnings, "agent_process_high_cpu")
	}
	if memoryHigh {
		s.Warnings = append(s.Warnings, "agent_process_high_memory")
	}
	if longCommands > 0 {
		s.Warnings = append(s.Warnings, "long_running_commands_observed")
	}
	return s
}
