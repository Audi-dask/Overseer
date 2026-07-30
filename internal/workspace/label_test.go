package workspace

import "testing"

func TestLabel(t *testing.T) {
	got := Label("/Users/mac/Desktop/PyMacbook/codereview/data/workspaces/devops__test-go-1e805d884ce4")
	want := "devops__test-go-1e805d884ce4"
	if got != want {
		t.Fatalf("Label() = %q, want %q", got, want)
	}
}

func TestRedactPaths(t *testing.T) {
	dir := "/data/workspaces/devops__test-go-abc"
	msg := "fatal: not a git repository (/data/workspaces/devops__test-go-abc/.git)"
	got := RedactPaths(dir, msg)
	want := "fatal: not a git repository (devops__test-go-abc/.git)"
	if got != want {
		t.Fatalf("RedactPaths() = %q, want %q", got, want)
	}
}
