package verification

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/weisyn/wesgine/tool"
)

type CompileError struct {
	Path    string
	Line    int
	Column  int
	Message string
}

type CompileResult struct {
	Success   bool
	Errors    []CompileError
	RawOutput string
	Duration  time.Duration
}

var goErrorPattern = regexp.MustCompile(`^(.+?):(\d+):(\d+): (.+)$`)

func RunGoBuild(ctx context.Context, dir string, sp tool.ShellProvider) CompileResult {
	start := time.Now()
	session, err := sp.Start(ctx, tool.ShellRequest{
		Command: "go build ./...",
		WorkDir: dir,
	})
	if err != nil {
		return CompileResult{
			Success:   false,
			RawOutput: "failed to start build: " + err.Error(),
			Duration:  time.Since(start),
		}
	}

	output, _ := io.ReadAll(session.Output())
	exitCode, _ := session.Wait()
	duration := time.Since(start)

	if exitCode == 0 {
		return CompileResult{Success: true, Duration: duration}
	}

	errors := parseGoErrors(string(output), dir)
	return CompileResult{
		Success:   false,
		Errors:    errors,
		RawOutput: string(output),
		Duration:  duration,
	}
}

func parseGoErrors(output, baseDir string) []CompileError {
	var errors []CompileError
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		matches := goErrorPattern.FindStringSubmatch(line)
		if len(matches) == 5 {
			var lineNum, colNum int
			fmt.Sscanf(matches[2], "%d", &lineNum)
			fmt.Sscanf(matches[3], "%d", &colNum)

			path := matches[1]
			if !filepath.IsAbs(path) {
				path = filepath.Join(baseDir, path)
			}

			errors = append(errors, CompileError{
				Path:    path,
				Line:    lineNum,
				Column:  colNum,
				Message: matches[4],
			})
		}
	}
	return errors
}
