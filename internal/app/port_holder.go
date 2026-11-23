package app

import (
	"bufio"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// portHolderLookup allows unit tests to stub OS process inspection.
var portHolderLookup = lookupPortHolderOS

// lookupPortHolder best-effort names the local process listening on port.
// It runs only once at bind-fallback time with a tight timeout and returns ""
// whenever the OS or permissions hide the process.
func lookupPortHolder(port int) string {
	if port < 1 || port > 65535 || portHolderLookup == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return sanitizeHolderName(portHolderLookup(ctx, port))
}

func lookupPortHolderOS(ctx context.Context, port int) string {
	switch runtime.GOOS {
	case "linux":
		if name := lookupPortHolderProc(port); name != "" {
			return name
		}
		return lookupPortHolderLsof(ctx, port)
	case "darwin", "freebsd", "openbsd", "netbsd":
		return lookupPortHolderLsof(ctx, port)
	case "windows":
		return lookupPortHolderWindows(ctx, port)
	default:
		return ""
	}
}

func lookupPortHolderProc(port int) string {
	wantPort := fmt.Sprintf("%04X", port)
	inodes := map[string]struct{}{}
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		f, err := os.Open(table)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(f)
		first := true
		for scanner.Scan() {
			if first {
				first = false
				continue
			}
			fields := strings.Fields(scanner.Text())
			// local_address rem_address st ... inode
			if len(fields) < 10 || fields[3] != "0A" {
				continue
			}
			_, hexPort, ok := strings.Cut(fields[1], ":")
			if ok && strings.EqualFold(hexPort, wantPort) && fields[9] != "0" {
				inodes["socket:["+fields[9]+"]"] = struct{}{}
			}
		}
		_ = f.Close()
	}
	if len(inodes) == 0 {
		return ""
	}
	selfPID := strconv.Itoa(os.Getpid())
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return ""
	}
	for _, entry := range procs {
		pid := entry.Name()
		if !entry.IsDir() || pid == selfPID || pid[0] < '0' || pid[0] > '9' {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", pid, "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join("/proc", pid, "fd", fd.Name()))
			if err != nil {
				continue
			}
			if _, match := inodes[target]; match {
				comm, err := os.ReadFile(filepath.Join("/proc", pid, "comm"))
				if err == nil {
					return strings.TrimSpace(string(comm))
				}
			}
		}
	}
	return ""
}

func lookupPortHolderLsof(ctx context.Context, port int) string {
	out, err := exec.CommandContext(
		ctx,
		"lsof",
		"-nP",
		fmt.Sprintf("-iTCP:%d", port),
		"-sTCP:LISTEN",
		"-Fpc",
	).Output()
	if err != nil {
		return ""
	}
	return parseLsofHolder(string(out), os.Getpid())
}

func parseLsofHolder(output string, ignorePID int) string {
	skip := false
	ignore := strconv.Itoa(ignorePID)
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			skip = line[1:] == ignore
		case 'c':
			if skip {
				continue
			}
			name := strings.ReplaceAll(line[1:], `\x20`, " ")
			if name = sanitizeHolderName(name); name != "" {
				return name
			}
		}
	}
	return ""
}

func lookupPortHolderWindows(ctx context.Context, port int) string {
	out, err := exec.CommandContext(ctx, "netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return ""
	}
	pid := parseNetstatListeningPID(string(out), port, os.Getpid())
	if pid == "" {
		return ""
	}
	taskOut, err := exec.CommandContext(ctx, "tasklist", "/FI", "PID eq "+pid, "/FO", "CSV", "/NH").Output()
	if err != nil {
		return ""
	}
	return parseTasklistCSV(string(taskOut))
}

func parseNetstatListeningPID(output string, port int, ignorePID int) string {
	suffix := ":" + strconv.Itoa(port)
	ignore := strconv.Itoa(ignorePID)
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		// Proto Local-Address Foreign-Address State PID
		if len(fields) < 5 || !strings.EqualFold(fields[0], "TCP") {
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) || !strings.EqualFold(fields[3], "LISTENING") {
			continue
		}
		pid := fields[4]
		if pid != "0" && pid != ignore {
			return pid
		}
	}
	return ""
}

func parseTasklistCSV(output string) string {
	r := csv.NewReader(strings.NewReader(strings.TrimSpace(output)))
	record, err := r.Read()
	if err != nil || len(record) == 0 {
		return ""
	}
	name := strings.TrimSpace(record[0])
	if strings.HasPrefix(strings.ToUpper(name), "INFO:") {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSuffix(name, ".exe"), ".EXE")
}

func sanitizeHolderName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
		if b.Len() >= 64 {
			break
		}
	}
	return strings.TrimSpace(b.String())
}
