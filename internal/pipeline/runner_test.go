package pipeline

import (
	"strings"
	"testing"

	"github.com/Audi-dask/Overseer/internal/vcs"
)

func TestFormatMRSummaryUsesDiscussionsForPostedFindings(t *testing.T) {
	all := []vcs.InlineComment{
		{Path: "a.go", StartLine: 10, Content: "first detail", Severity: "high", Category: "bug"},
		{Path: "b.go", StartLine: 20, Content: "second detail", Severity: "medium", Category: "bug"},
	}
	got := formatMRSummary(all, nil)
	if !strings.Contains(got, "共 **2** 条意见（high 1 / medium 1 / low 0）") {
		t.Fatalf("missing summary counts: %s", got)
	}
	if !strings.Contains(got, "发布 **2** 条 Discussion") {
		t.Fatalf("missing discussion count: %s", got)
	}
	if strings.Contains(got, "first detail") || strings.Contains(got, "second detail") {
		t.Fatalf("posted finding details should not be duplicated: %s", got)
	}
}

func TestFormatMRSummaryExpandsFallbackFindings(t *testing.T) {
	posted := vcs.InlineComment{Path: "a.go", StartLine: 10, Content: "posted detail", Severity: "high"}
	fallback := vcs.InlineComment{
		Path: "b.go", StartLine: 20, EndLine: 22, Content: "fallback detail",
		SuggestionCode: "return err", Severity: "medium", Category: "bug",
	}
	got := formatMRSummary([]vcs.InlineComment{posted, fallback}, []vcs.InlineComment{fallback})
	if !strings.Contains(got, "发布 **1** 条 Discussion") || !strings.Contains(got, "以下 **1** 条") {
		t.Fatalf("missing posted/fallback counts: %s", got)
	}
	if !strings.Contains(got, "`b.go:20-22`") || !strings.Contains(got, "fallback detail") || !strings.Contains(got, "return err") {
		t.Fatalf("fallback detail missing: %s", got)
	}
	if strings.Contains(got, "posted detail") {
		t.Fatalf("posted finding detail should not be duplicated: %s", got)
	}
}

func TestFormatMRSummaryNoFindings(t *testing.T) {
	got := formatMRSummary(nil, nil)
	if !strings.Contains(got, "未发现需要评论的问题") {
		t.Fatalf("unexpected empty summary: %s", got)
	}
}
