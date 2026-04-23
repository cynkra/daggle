package executor

import (
	"slices"
	"strings"
	"testing"

	"github.com/cynkra/daggle/dag"
)

func TestBuildDockerArgs_MinimalCommand(t *testing.T) {
	args := buildDockerArgs(&dag.DockerStep{
		Image:   "alpine:3.20",
		Command: "echo hi",
	})
	want := []string{"run", "--rm", "alpine:3.20", "sh", "-c", "echo hi"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %v, want %v", args, want)
	}
}

func TestBuildDockerArgs_AllOptions(t *testing.T) {
	args := buildDockerArgs(&dag.DockerStep{
		Image:      "bioconductor/bioconductor_docker:3.18",
		Command:    "Rscript /work/run.R",
		Entrypoint: "/bin/sh",
		Volumes:    []string{"./data:/work/data:ro", "./out:/work/out"},
		Env:        map[string]string{"PROJECT": "X", "API_KEY": "secret"},
		Workdir:    "/work",
		Network:    "host",
		User:       "1000:1000",
		Pull:       "always",
	})

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"run --rm",
		"--pull always",
		"--network host",
		"--user 1000:1000",
		"--workdir /work",
		"--entrypoint /bin/sh",
		"-v ./data:/work/data:ro",
		"-v ./out:/work/out",
		"-e API_KEY=secret",
		"-e PROJECT=X",
		"bioconductor/bioconductor_docker:3.18 sh -c Rscript /work/run.R",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected %q in args:\n%s", want, joined)
		}
	}
}

func TestBuildDockerArgs_EntrypointOnly(t *testing.T) {
	// entrypoint set, no command — the image should still be the last arg.
	args := buildDockerArgs(&dag.DockerStep{
		Image:      "myimg:latest",
		Entrypoint: "/run.sh",
	})
	if args[len(args)-1] != "myimg:latest" {
		t.Errorf("image should be the last arg, got %v", args)
	}
}

func TestBuildDockerArgs_EnvStableOrder(t *testing.T) {
	a := buildDockerArgs(&dag.DockerStep{
		Image: "i", Command: "c",
		Env: map[string]string{"X": "1", "Y": "2", "Z": "3"},
	})
	b := buildDockerArgs(&dag.DockerStep{
		Image: "i", Command: "c",
		Env: map[string]string{"Z": "3", "Y": "2", "X": "1"},
	})
	if !slices.Equal(a, b) {
		t.Errorf("env-arg order should be stable across map iteration\na=%v\nb=%v", a, b)
	}
}
