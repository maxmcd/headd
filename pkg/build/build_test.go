package build

import (
	"io"
	"os"
	"testing"
)

func TestNewBuild(t *testing.T) {
	build, err := NewBuild(Cmd{
		Cmd:  "cat",
		Args: []string{"./build.go"},
	})
	if err != nil {
		t.Fatalf("NewBuild() error = %v", err)
	}

	f, err := os.Open(build.log.Name())
	if err != nil {
		t.Fatalf("opening log file: %v", err)
	}
	defer f.Close()

	_, _ = io.Copy(os.Stdout, f)
}
