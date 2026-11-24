package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigYAMLAndJSON(t *testing.T) {
	dir := t.TempDir()
	y := filepath.Join(dir, "c.yaml")
	os.WriteFile(y, []byte("# comment\nmission_weight: 9\nmission_terms:\n  - soil\n"), 0o644)
	c, err := loadConfig(y)
	if err != nil || c.MissionWeight != 9 || len(c.MissionTerms) != 1 || c.SalaryFloor != 170000 {
		t.Fatalf("yaml: %+v %v", c, err)
	}
	j := filepath.Join(dir, "c.json")
	os.WriteFile(j, []byte(`{"no_mission_penalty": 5}`), 0o644)
	if c, err := loadConfig(j); err != nil || c.NoMissionPenalty != 5 || c.MissionWeight != 6 {
		t.Fatalf("json: %+v %v", c, err)
	}
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("misson_weight: 9\n"), 0o644)
	if _, err := loadConfig(bad); err == nil {
		t.Fatal("unknown key accepted")
	}
	empty := filepath.Join(dir, "empty.yaml")
	os.WriteFile(empty, nil, 0o644)
	if c, err := loadConfig(empty); err != nil || c.MissionWeight != 6 {
		t.Fatalf("empty: %+v %v", c, err)
	}
}

func TestConfigPathOrder(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	xdg := filepath.Join(dir, "xdg")
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("JOBSCAN_CONFIG", "")

	if got := configPath(""); got != "" {
		t.Fatalf("nothing present: got %q", got)
	}
	user := filepath.Join(xdg, "jobscan", "config.yaml")
	os.MkdirAll(filepath.Dir(user), 0o755)
	os.WriteFile(user, nil, 0o644)
	if got := configPath(""); got != user {
		t.Fatalf("user file: got %q", got)
	}
	os.WriteFile("config.yaml", nil, 0o644)
	if got := configPath(""); got != "config.yaml" {
		t.Fatalf("local file should win over user file: got %q", got)
	}
	t.Setenv("JOBSCAN_CONFIG", "env.yaml")
	if got := configPath(""); got != "env.yaml" {
		t.Fatalf("env should win: got %q", got)
	}
	if got := configPath("flag.yaml"); got != "flag.yaml" {
		t.Fatalf("flag should win: got %q", got)
	}
}

func TestConfigFlag(t *testing.T) {
	cases := map[string][]string{
		"a.yaml": {"-sizes", "1,2", "-config", "a.yaml"},
		"b.yaml": {"--config=b.yaml", "-out", "x"},
		"":       {"-out", "x", "--", "-config", "c.yaml"},
	}
	for want, args := range cases {
		if got := configFlag(args); got != want {
			t.Errorf("%v: got %q want %q", args, got, want)
		}
	}
}

func TestExampleConfigLoads(t *testing.T) {
	c, err := loadConfig("config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Candidate.Location == "" || len(c.Candidate.Interests) == 0 || len(c.Boards.Browser) == 0 {
		t.Errorf("example should show candidate and browser boards: %+v", c.Candidate)
	}
}

func TestWriteCriteria(t *testing.T) {
	p := filepath.Join(t.TempDir(), "reviews", "criteria.md")
	c := defaultConfig()
	if err := writeCriteria(p, "", c); err == nil {
		t.Fatal("want error without candidate location/interests")
	}
	c.Candidate = Candidate{Location: "Denver, CO", Interests: []string{"water", "soil"}}
	c.ExcludeTerms = []string{"carbon credit"}
	c.SalaryFloor = 150000
	if err := writeCriteria(p, "my.yaml", c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"from my.yaml", "Denver, CO", "$150000", "(1) water, (2) soil, (3) none of these", "carbon credit"} {
		if !strings.Contains(s, want) {
			t.Errorf("criteria missing %q:\n%s", want, s)
		}
	}
}
