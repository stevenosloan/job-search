package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestMain points every test at an empty config, so no test reads the
// user's own config.yaml or ~/.config/jobscan/config.yaml.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "jobscan-test")
	if err != nil {
		panic(err)
	}
	empty := filepath.Join(dir, "empty.yaml")
	os.WriteFile(empty, nil, 0o644)
	os.Setenv("JOBSCAN_CONFIG", empty)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// chdir moves into dir for one test (t.Chdir needs Go 1.24; module is 1.21).
func chdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
}

func TestScoreMissionWeightAndPenalty(t *testing.T) {
	c := defaultConfig()
	c.MissionTerms = []string{"soil"}
	c.MissionWeight, c.NoMissionPenalty = 10, 25
	base := Job{Title: "Senior Engineer", Company: "X", SalaryMax: json.RawMessage(`200000`), Size: "201-500"}
	match := base
	match.Description = "soil health maps"
	rc, _, _ := score(match, c)
	rg, _, _ := score(base, c)
	if rc.Score-rg.Score != 35 {
		t.Errorf("want 35-point gap (10 mission + 25 penalty), got %d vs %d", rc.Score, rg.Score)
	}
	if rg.Flags != "no mission match" {
		t.Errorf("flags = %q", rg.Flags)
	}
	// Defaults keep the old behaviour: 6 per hit, no penalty.
	d := defaultConfig()
	d.MissionTerms = []string{"soil"}
	dc, _, _ := score(match, d)
	dg, _, _ := score(base, d)
	if dc.Score-dg.Score != 6 || dg.Flags != "" {
		t.Errorf("defaults changed: %d vs %d, flags %q", dc.Score, dg.Score, dg.Flags)
	}
}

func TestCompanyKey(t *testing.T) {
	for _, n := range []string{"Afresh", "Afresh Technologies", "afresh technologies, inc.", " Afresh LLC "} {
		if k := companyKey(n); k != "afresh" {
			t.Errorf("companyKey(%q) = %q", n, k)
		}
	}
	if k := companyKey("Technologies"); k != "technologies" {
		t.Errorf("single-word name stripped: %q", k)
	}
}
