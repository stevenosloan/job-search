package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestExampleConfigLoads(t *testing.T) {
	c, err := loadConfig("config.example.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if c.Candidate.Location == "" || len(c.Candidate.Interests) == 0 || len(c.Boards.Browser) == 0 {
		t.Errorf("example should show candidate and browser boards: %+v", c.Candidate)
	}
}

func TestInputFiles(t *testing.T) {
	chdir(t, t.TempDir())
	c := defaultConfig()
	c.Boards.Browser = []BrowserBoard{{Name: "x", URL: "u", Out: "postings-x.json", Enabled: true}}
	for _, f := range []string{"postings-cb.json", "postings-x.json"} {
		os.WriteFile(f, []byte("[]"), 0o644)
	}
	got := strings.Join(c.inputFiles(), " ")
	if got != "postings-x.json" {
		t.Errorf("want only enabled, present files; got %q", got)
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
