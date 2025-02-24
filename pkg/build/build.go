package build

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/maxmcd/headd/pkg/linewriter"
)

type Build struct {
	// Dir is the directory where the build output will be stored.
	// If not set, it will be created in the user cache directory.
	Dir string
	// ID is the unique identifier for the build.
	// If not set, it will be generated using the current timestamp.
	ID string
	// Tee is used to stream log output.. Logs are streamed in Line-delimited
	// JSON:
	//
	//     {"msg":"+ echo hi","stream":"stderr","ts":"2025-02-22T13:37:02.279744-05:00"}
	//     {"msg":"+ cat hi.txt","stream":"stderr","ts":"2025-02-22T13:37:02.279942-05:00"}
	//     {"msg":"hi","stream":"stdout","ts":"2025-02-22T13:37:02.281331-05:00"}
	Tee io.Writer
}

type Cmd struct {
	Cmd  string
	Args []string
	Env  []string
}

func getBuildOutputDir(id string) (string, error) {
	// Get user cache directory
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}

	// Create base headd builds directory if it doesn't exist
	headdBuildsDir := filepath.Join(cacheDir, "headd", "builds")
	if err := os.MkdirAll(headdBuildsDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create builds directory: %w", err)
	}

	// Generate unique build ID using timestamp
	buildDir := filepath.Join(headdBuildsDir, id)

	// Create the unique build directory
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create build directory: %w", err)
	}

	return buildDir, nil
}

func (b *Build) Run(cmd Cmd) error {
	if b.ID == "" {
		b.ID = fmt.Sprintf("%d", time.Now().UnixNano())
	}
	if b.Dir == "" {
		dir, err := getBuildOutputDir(b.ID)
		if err != nil {
			return fmt.Errorf("failed to get build output directory: %w", err)
		}
		b.Dir = dir
	}

	f, err := os.Create(filepath.Join(b.Dir, "build.log"))
	if err != nil {
		return fmt.Errorf("creating build log file: %w", err)
	}
	defer f.Close()
	var w io.Writer = f
	if b.Tee != nil {
		w = io.MultiWriter(w, b.Tee)
	}
	writer := bufio.NewWriter(w)
	defer writer.Flush()
	encoder := json.NewEncoder(writer)
	lock := sync.Mutex{}
	writeLine := func(stream string) func(p []byte) {
		return func(p []byte) {
			lock.Lock()
			defer lock.Unlock()
			_ = encoder.Encode(map[string]string{
				"ts":     time.Now().Format(time.RFC3339Nano),
				"stream": stream,
				"msg":    string(p),
			})
		}
	}
	stderrWriter := linewriter.NewWriter(writeLine("stderr"))
	stdoutWriter := linewriter.NewWriter(writeLine("stdout"))

	c := exec.Command(cmd.Cmd, cmd.Args...)
	c.Env = append(os.Environ(), cmd.Env...)
	c.Dir = b.Dir
	c.Stdout = stdoutWriter
	c.Stderr = stderrWriter

	if err := c.Run(); err != nil {
		return fmt.Errorf("running command: %w", err)
	}

	return nil
}
