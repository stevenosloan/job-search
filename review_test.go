package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadReviewsValidates(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "a.json")
	os.WriteFile(good, []byte(`[{"url":"u1","fit":70,"location_ok":false,"gaps":["x"]},{"url":"u2","fit":40}]`), 0o644)
	later := filepath.Join(dir, "b.json")
	os.WriteFile(later, []byte(`[{"url":"u2","fit":90}]`), 0o644)
	rs, err := loadReviews([]string{good, " " + later})
	if err != nil || len(rs) != 2 || rs["u2"].Fit != 90 || *rs["u1"].LocationOK {
		t.Fatalf("got %+v %v", rs, err)
	}
	for name, body := range map[string]string{
		"nourl.json": `[{"fit":50}]`,
		"range.json": `[{"url":"u","fit":120}]`,
		"bad.json":   `[{"url":"u","fit":50},]`,
	} {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := loadReviews([]string{p}); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestApplyReviewsOrdering(t *testing.T) {
	no := false
	ranked := []Result{
		{Score: 80, Job: Job{URL: "kw-top"}},
		{Score: 60, Job: Job{URL: "rev-low"}},
		{Score: 50, Job: Job{URL: "rev-high"}, Flags: "size unknown"},
		{Score: 40, Job: Job{URL: "rev-tie"}},
		{Score: 30, Job: Job{URL: "kw-low"}},
	}
	reviews := map[string]Review{
		"rev-low":  {URL: "rev-low", Fit: 40},
		"rev-high": {URL: "rev-high", Fit: 75, LocationOK: &no},
		"rev-tie":  {URL: "rev-tie", Fit: 40},
		"orphan":   {URL: "orphan", Fit: 99},
	}
	got := applyReviews(ranked, reviews)
	var order []string
	for _, r := range got {
		order = append(order, r.Job.URL)
	}
	want := "rev-high rev-low rev-tie kw-top kw-low"
	if strings.Join(order, " ") != want {
		t.Fatalf("order %v, want %s", order, want)
	}
	if got[0].Flags != "size unknown; location doesn't work" {
		t.Errorf("flags %q", got[0].Flags)
	}
}

func TestBuildBatchTopAndTruncate(t *testing.T) {
	ranked := []Result{
		{Score: 9, Job: Job{URL: "a", Description: strings.Repeat("é", 10)}},
		{Score: 8, Job: Job{URL: "b", Description: "éééé"}}, // 8 bytes, 4 runes: not cut
		{Score: 7, Job: Job{URL: "c"}},
	}
	items := buildBatch(ranked, 2, 4)
	if len(items) != 2 || items[0].KeywordScore != 9 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Description != "éééé [truncated]" || items[1].Description != "éééé" {
		t.Errorf("descriptions %q / %q", items[0].Description, items[1].Description)
	}
}

func TestMdCell(t *testing.T) {
	if got := mdCell("a|b\nc"); got != "a/b c" {
		t.Errorf("mdCell = %q", got)
	}
}

func TestCheckBatchCoverage(t *testing.T) {
	dir := t.TempDir()
	batch := filepath.Join(dir, "batch.json")
	os.WriteFile(batch, []byte(`[{"url":"a","company":"A"},{"url":"b","company":"B"},{"url":"c","company":"C"}]`), 0o644)
	missing := filepath.Join(dir, "batch-missing.json")
	if missingPath(batch) != missing {
		t.Fatalf("missingPath = %s", missingPath(batch))
	}

	// Reviewer skipped b and altered c's URL.
	reviews := map[string]Review{"a": {URL: "a"}, "c-typo": {URL: "c-typo"}}
	if err := checkBatchCoverage(batch, reviews); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(missing)
	if err != nil {
		t.Fatal(err)
	}
	if s := string(b); !strings.Contains(s, `"url": "b"`) || !strings.Contains(s, `"url": "c"`) || strings.Contains(s, `"url": "a"`) {
		t.Errorf("missing file:\n%s", s)
	}

	// Full coverage removes the stale missing file.
	reviews["b"], reviews["c"] = Review{URL: "b"}, Review{URL: "c"}
	if err := checkBatchCoverage(batch, reviews); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Errorf("stale missing file kept: %v", err)
	}
}
