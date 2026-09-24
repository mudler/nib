package loop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/mudler/nib/internal/proc"
)

const (
	maxDiffChars   = 4000
	maxOutputChars = 8000
	urlTimeout     = 30 * time.Second
	maxURLBytes    = 256 * 1024
	scriptTimeout  = 30 * time.Second
)

// MonitorResult is the output of a monitor check.
type MonitorResult struct {
	Output string
	Hash   string
	Err    error
}

// RunMonitor executes the monitor source (script or URL) and returns the capped
// output with its SHA-256 hash. Returns an error result when no source is
// configured or the check fails.
func RunMonitor(m MonitorConfig) MonitorResult {
	if m.Script != "" {
		out, err := runMonitorScript(m.Script)
		if err != nil {
			return MonitorResult{Err: err}
		}
		capped := capOutput(out)
		return MonitorResult{Output: capped, Hash: hashOutput(capped)}
	}
	if m.URL != "" {
		out, err := runMonitorURL(m.URL)
		if err != nil {
			return MonitorResult{Err: err}
		}
		capped := capOutput(out)
		return MonitorResult{Output: capped, Hash: hashOutput(capped)}
	}
	return MonitorResult{Err: fmt.Errorf("no monitor source configured")}
}

func runMonitorScript(script string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	proc.Group(cmd)
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("monitor script failed: %w", err)
	}
	return string(out), nil
}

func runMonitorURL(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), urlTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", fmt.Errorf("monitor URL: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("monitor URL: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("monitor URL: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxURLBytes+1))
	if err != nil {
		return "", fmt.Errorf("monitor URL: %w", err)
	}
	if len(body) > maxURLBytes {
		return "", fmt.Errorf("monitor URL: body exceeds %d bytes", maxURLBytes)
	}
	return string(body), nil
}

func capOutput(s string) string {
	if len(s) > maxOutputChars {
		return s[:maxOutputChars]
	}
	return s
}

func hashOutput(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Diff returns a unified-diff-style string showing changed lines between old
// and new. Returns "" when old is empty (first run). The output is capped at
// maxDiffChars.
func Diff(old, new string) string {
	if old == "" {
		return ""
	}
	oldLines := splitLines(old)
	newLines := splitLines(new)
	var b strings.Builder
	b.WriteString("--- previous\n+++ current\n")
	shown := 0
	for i := 0; i < len(oldLines) && shown < maxDiffChars; i++ {
		if i >= len(newLines) || oldLines[i] != newLines[i] {
			if i < len(oldLines) {
				b.WriteString("-" + oldLines[i] + "\n")
				shown += len(oldLines[i]) + 2
			}
			if i < len(newLines) {
				b.WriteString("+" + newLines[i] + "\n")
				shown += len(newLines[i]) + 2
			}
		}
	}
	for i := len(oldLines); i < len(newLines) && shown < maxDiffChars; i++ {
		b.WriteString("+" + newLines[i] + "\n")
		shown += len(newLines[i]) + 2
	}
	if shown >= maxDiffChars {
		b.WriteString("... (truncated)\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}
