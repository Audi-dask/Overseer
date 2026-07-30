package firewall_test

import (
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/firewall"
)

func TestFilterDiff(t *testing.T) {
	diff := `--- a/main.go
+++ b/main.go
@@
+package main

--- a/package-lock.json
+++ b/package-lock.json
@@
+{}
`
	rules := "**/package-lock.json\n"
	out := firewall.FilterDiff(diff, rules)
	if strings.Contains(out, "package-lock.json") {
		t.Fatalf("lockfile should be filtered: %s", out)
	}
	if !strings.Contains(out, "main.go") {
		t.Fatalf("main.go should remain: %s", out)
	}
}

func TestIsExcludedWithNegation(t *testing.T) {
	rules := "*.env\nconfig/**\n!config/public.yaml\n"
	if !firewall.IsExcluded(".env", rules) {
		t.Fatal(".env should be excluded")
	}
	if !firewall.IsExcluded("config/secret.yaml", rules) {
		t.Fatal("config/secret.yaml should be excluded")
	}
	if firewall.IsExcluded("config/public.yaml", rules) {
		t.Fatal("negation should re-include config/public.yaml")
	}
}
