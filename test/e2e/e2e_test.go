//go:build e2e

// Package e2e runs e2e.sh against real anet daemons and a local test hub.
//
//	go test -tags e2e ./test/e2e -v -timeout 20m
//
// Binaries come from the environment:
//
//	ANET_ORG_E2E_ANET, ANET_ORG_E2E_HUB   prebuilt anet and anet-hub binaries; or
//	ANET_SRC                              an ANet checkout, built with go build ./cmd/anet
//	ANETHUB_SRC, ANETHUB_REF              an ANetHub repository and the commit or branch to build;
//	                                      it is checked out in a temporary git worktree, which is
//	                                      removed afterwards
//
// Everything is built into a temporary directory, never into a source tree.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func run(t *testing.T, dir string, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
}

func TestEndToEnd(t *testing.T) {
	bin := t.TempDir()
	anet := os.Getenv("ANET_ORG_E2E_ANET")
	hub := os.Getenv("ANET_ORG_E2E_HUB")
	if anet == "" {
		src := os.Getenv("ANET_SRC")
		if src == "" {
			t.Skip("set ANET_ORG_E2E_ANET or ANET_SRC")
		}
		anet = filepath.Join(bin, "anet")
		run(t, src, nil, "go", "build", "-o", anet, "./cmd/anet")
	}
	if hub == "" {
		repo, ref := os.Getenv("ANETHUB_SRC"), os.Getenv("ANETHUB_REF")
		if repo == "" || ref == "" {
			t.Skip("set ANET_ORG_E2E_HUB or ANETHUB_SRC and ANETHUB_REF")
		}
		wt := filepath.Join(bin, "hubsrc")
		run(t, repo, nil, "git", "worktree", "add", "--detach", wt, ref)
		t.Cleanup(func() {
			exec.Command("git", "-C", repo, "worktree", "remove", "--force", wt).Run()
			exec.Command("git", "-C", repo, "worktree", "prune").Run()
		})
		hub = filepath.Join(bin, "anet-hub")
		run(t, wt, nil, "go", "build", "-o", hub, "./cmd/anet-hub")
	}
	org := filepath.Join(bin, "anet-org")
	run(t, "../..", nil, "go", "build", "-o", org, "./cmd/anet-org")

	cmd := exec.Command("bash", "e2e.sh", "--anet", anet, "--hub", hub, "--org", org)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("e2e.sh: %v", err)
	}
}
