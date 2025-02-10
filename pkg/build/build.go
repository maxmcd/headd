package build

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/maxmcd/headd/pkg/linewriter"
)

type Build struct {
	dir string
	log *os.File
}

type Cmd struct {
	Cmd  string
	Args []string
	Env  []string
}

func NewBuild(cmd Cmd) (*Build, error) {
	dir, err := os.MkdirTemp("", "build")
	if err != nil {
		return nil, fmt.Errorf("creating build dir: %w", err)
	}

	f, err := os.Create(filepath.Join(dir, "build.log"))
	if err != nil {
		return nil, fmt.Errorf("creating build log file: %w", err)
	}
	defer f.Close()
	writer := bufio.NewWriter(f)
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
	c.Stdout = stdoutWriter
	c.Stderr = stderrWriter

	if err := c.Run(); err != nil {
		return nil, fmt.Errorf("running command: %w", err)
	}

	return &Build{dir: dir, log: f}, nil
}
