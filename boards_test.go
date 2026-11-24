package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jobscan/boards/browser"
)

func TestLoadConfigBoards(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.yaml")
	os.WriteFile(p, []byte(`
boards:
  climatebase:
    enabled: true
  remote_rocketship:
    titles: ["Staff Engineer"]
  browser:
    - name: terra
      url: https://example.org/jobs
`), 0o644)
	c, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Boards.Climatebase.Enabled || c.Boards.Climatebase.Out != "postings-cb.json" {
		t.Errorf("climatebase: %+v", c.Boards.Climatebase)
	}
	rr := c.Boards.RemoteRocketship
	if !rr.Enabled || len(rr.Titles) != 1 || rr.MaxJobs != 1000 || rr.Out != "postings-rr.json" {
		t.Errorf("remote_rocketship should keep unset defaults: %+v", rr)
	}
	if b := c.Boards.Browser; len(b) != 1 || b[0].Out != "postings-terra.json" {
		t.Errorf("browser default out: %+v", b)
	}

	bad := map[string]string{
		"unknown key": "boards:\n  climatebase:\n    enabeld: true\n",
		"no url":      "boards:\n  browser:\n    - name: x\n",
		"same out":    "boards:\n  browser:\n    - name: x\n      url: u\n      out: postings-rr.json\n",
	}
	for name, body := range bad {
		os.WriteFile(p, []byte(body), 0o644)
		if _, err := loadConfig(p); err == nil {
			t.Errorf("%s: want error", name)
		}
	}
}

func TestInputFiles(t *testing.T) {
	chdir(t, t.TempDir())
	c := defaultConfig()
	c.Boards.Browser = []browser.Board{{Name: "x", URL: "u", Out: "postings-x.json", Enabled: true}}
	for _, f := range []string{"postings-cb.json", "postings-x.json"} {
		os.WriteFile(f, []byte("[]"), 0o644)
	}
	got := strings.Join(c.inputFiles(), " ")
	if got != "postings-x.json" {
		t.Errorf("want only enabled, present files; got %q", got)
	}
}
