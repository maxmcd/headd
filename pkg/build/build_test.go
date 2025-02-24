package build

import (
	"fmt"
	"os"
	"testing"
)

func TestNewBuild(t *testing.T) {
	build := &Build{
		Tee: os.Stdout,
	}
	if err := build.Run(Cmd{
		Cmd: "bash",
		Args: []string{"-c", `
		set -ex
		echo hi > hi.txt
		cat hi.txt
		`},
	}); err != nil {
		t.Fatalf("build.Run() error = %v", err)
	}

	fmt.Println(build.Dir)

}
