package main

// Per-user settings: which job boards to search (and how), the candidate
// profile the posting-reviewer reads, and where the config file lives.

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Boards lists the job boards a run collects from. Climatebase (a climate
// jobs board, off by default) is read in a browser, Remote Rocketship through
// its API, and Browser holds any other board Claude reads in a browser.
type Boards struct {
	Climatebase      CBBoard        `json:"climatebase" yaml:"climatebase"`
	RemoteRocketship RRBoard        `json:"remote_rocketship" yaml:"remote_rocketship"`
	Browser          []BrowserBoard `json:"browser" yaml:"browser"`
}

type CBBoard struct {
	Enabled   bool     `json:"enabled" yaml:"enabled"`
	Out       string   `json:"out" yaml:"out"`
	Sizes     []string `json:"sizes" yaml:"sizes"`
	Sectors   []string `json:"sectors" yaml:"sectors"`
	Nonprofit bool     `json:"nonprofit" yaml:"nonprofit"`
}

type RRBoard struct {
	Enabled    bool     `json:"enabled" yaml:"enabled"`
	Out        string   `json:"out" yaml:"out"`
	Titles     []string `json:"titles" yaml:"titles"`
	Locations  []string `json:"locations" yaml:"locations"`
	Seniority  []string `json:"seniority" yaml:"seniority"`
	Sizes      []string `json:"sizes" yaml:"sizes"` // each "min,max" is its own request series
	Keywords   []string `json:"keywords" yaml:"keywords"`
	Exclude    []string `json:"exclude" yaml:"exclude"`
	Tech       []string `json:"tech" yaml:"tech"`
	Industries []string `json:"industries" yaml:"industries"`
	MinSalary  int      `json:"min_salary" yaml:"min_salary"`
	MaxPages   int      `json:"max_pages" yaml:"max_pages"`
	MaxJobs    int      `json:"max_jobs" yaml:"max_jobs"`
}

type BrowserBoard struct {
	Name    string `json:"name" yaml:"name"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	URL     string `json:"url" yaml:"url"`
	Out     string `json:"out" yaml:"out"`
	Notes   string `json:"notes" yaml:"notes"`
}

// Candidate is what the posting-reviewer judges against, alongside
// salary_floor, size_min/size_max and exclude_terms.
type Candidate struct {
	Roles     string   `json:"roles" yaml:"roles"`
	Strengths string   `json:"strengths" yaml:"strengths"`
	NotAFit   string   `json:"not_a_fit" yaml:"not_a_fit"`
	Location  string   `json:"location" yaml:"location"`
	Interests []string `json:"interests" yaml:"interests"` // in order; index+1 is mission_tier
	Notes     string   `json:"notes" yaml:"notes"`
}

func defaultBoards() Boards {
	return Boards{
		Climatebase: CBBoard{Enabled: false, Out: "postings-cb.json", Sizes: []string{"101-250", "251-500"}},
		RemoteRocketship: RRBoard{
			Enabled: true, Out: "postings-rr.json",
			Titles:    []string{"Software Engineer", "Frontend Engineer", "Full Stack Engineer"},
			Locations: []string{"United States"}, Seniority: []string{"senior", "expert"},
			Sizes:     []string{"51,200", "201,500"},
			MinSalary: 170000, MaxPages: 6, MaxJobs: 1000,
		},
	}
}

// checkBoards fills browser-board defaults and rejects entries that can't be
// collected or would overwrite another board's file.
func (c *Config) checkBoards() error {
	outs := map[string]string{}
	claim := func(name, out string) error {
		if prev, ok := outs[out]; ok {
			return fmt.Errorf("boards %s and %s both write %s", prev, name, out)
		}
		outs[out] = name
		return nil
	}
	if err := claim("climatebase", c.Boards.Climatebase.Out); err != nil {
		return err
	}
	if err := claim("remote_rocketship", c.Boards.RemoteRocketship.Out); err != nil {
		return err
	}
	for i := range c.Boards.Browser {
		b := &c.Boards.Browser[i]
		if b.Name == "" || b.URL == "" {
			return fmt.Errorf("boards.browser[%d] needs a name and url", i)
		}
		if b.Out == "" {
			b.Out = "postings-" + b.Name + ".json"
		}
		if err := claim(b.Name, b.Out); err != nil {
			return err
		}
	}
	return nil
}

type boardInfo struct {
	Name, Method, Out string
	Enabled           bool
	Detail            string
}

func (c Config) boardList() []boardInfo {
	cb, rr := c.Boards.Climatebase, c.Boards.RemoteRocketship
	l := []boardInfo{
		{"climatebase", "browser (Claude)", cb.Out, cb.Enabled, "./jobscan climatebase-url"},
		{"remote_rocketship", "API (user runs it)", rr.Out, rr.Enabled, "./jobscan fetch-rr"},
	}
	for _, b := range c.Boards.Browser {
		d := b.URL
		if b.Notes != "" {
			d += " (" + b.Notes + ")"
		}
		l = append(l, boardInfo{b.Name, "browser (Claude)", b.Out, b.Enabled, d})
	}
	return l
}

// inputFiles is the default postings list for score and review-batch: the
// output file of every enabled board that exists.
func (c Config) inputFiles() []string {
	var out []string
	for _, b := range c.boardList() {
		if !b.Enabled {
			continue
		}
		if _, err := os.Stat(b.Out); err == nil {
			out = append(out, b.Out)
		}
	}
	return out
}

func postingArgs(fs *flag.FlagSet, c Config) ([]string, error) {
	if fs.NArg() > 0 {
		return fs.Args(), nil
	}
	files := c.inputFiles()
	if len(files) == 0 {
		return nil, fmt.Errorf("no postings files given and no enabled board's file exists (see: jobscan boards)")
	}
	fmt.Fprintf(os.Stderr, "postings: %s\n", strings.Join(files, " "))
	return files, nil
}

func runBoards(args []string) error {
	fs := flag.NewFlagSet("boards", flag.ExitOnError)
	cfgFlag := fs.String("config", "", "config file (see configPath)")
	files := fs.Bool("files", false, "print only the enabled boards' postings files that exist, one per line")
	fs.Parse(args)
	c, path, err := resolveConfig(*cfgFlag)
	if err != nil {
		return err
	}
	if *files {
		for _, f := range c.inputFiles() {
			fmt.Println(f)
		}
		return nil
	}
	if path == "" {
		path = "(defaults)"
	}
	fmt.Printf("config: %s\n", path)
	for _, b := range c.boardList() {
		state := "on "
		if !b.Enabled {
			state = "off"
		}
		have := "missing"
		if _, err := os.Stat(b.Out); err == nil {
			have = "present"
		}
		fmt.Printf("  [%s] %-18s %-19s -> %s (%s)\n        %s\n", state, b.Name, b.Method, b.Out, have, b.Detail)
	}
	return nil
}

// userConfigPath is $XDG_CONFIG_HOME/jobscan/config.yaml, else
// ~/.config/jobscan/config.yaml.
func userConfigPath() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "jobscan", "config.yaml")
}

// configPath picks the config file: -config, then $JOBSCAN_CONFIG, then
// ./config.yaml (gitignored), then the per-user file. "" means defaults.
func configPath(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if e := os.Getenv("JOBSCAN_CONFIG"); e != "" {
		return e
	}
	for _, p := range []string{"config.yaml", userConfigPath()} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func resolveConfig(flagVal string) (Config, string, error) {
	p := configPath(flagVal)
	if p == "" {
		fmt.Fprintf(os.Stderr, "note: no config found; using built-in defaults. Copy config.example.yaml to config.yaml or %s.\n", userConfigPath())
	}
	c, err := loadConfig(p)
	return c, p, err
}

// configFlag finds -config in args before the FlagSet is built, so other
// flags can take their defaults from the config.
func configFlag(args []string) string {
	for i, a := range args {
		if a == "--" {
			break
		}
		if !strings.HasPrefix(a, "-") {
			continue
		}
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if name != "config" {
			continue
		}
		if hasVal {
			return val
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// writeCriteria renders the candidate profile for the posting-reviewer next
// to the batch, with defaults applied, so the agent never parses YAML.
func writeCriteria(path, cfgPath string, c Config) error {
	k := c.Candidate
	if k.Location == "" || len(k.Interests) == 0 {
		return fmt.Errorf("config needs candidate.location and candidate.interests for the reviewer (see config.example.yaml)")
	}
	src := cfgPath
	if src == "" {
		src = "built-in defaults"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Candidate criteria\n\nGenerated by `jobscan review-batch` from %s.\n\n", src)
	line := func(label, v string) {
		if v = strings.TrimSpace(v); v != "" {
			fmt.Fprintf(&sb, "- %s: %s\n", label, v)
		}
	}
	line("Roles", k.Roles)
	line("Strengths", k.Strengths)
	line("Not a fit", k.NotAFit)
	line("Location", k.Location)
	fmt.Fprintf(&sb, "- Company size %d-%d preferred. Base salary floor $%d: `pay_ok` is true when the posted top of range is at least that; unposted pay is unknown, not a fail.\n",
		c.SizeMin, c.SizeMax, c.SalaryFloor)
	if len(c.ExcludeTerms) > 0 {
		fmt.Fprintf(&sb, "- Exclude: %s, and similar.\n", strings.Join(c.ExcludeTerms, ", "))
	}
	sb.WriteString("- Mission interest, in order (this is `mission_tier`):")
	for i, in := range k.Interests {
		fmt.Fprintf(&sb, " (%d) %s,", i+1, in)
	}
	fmt.Fprintf(&sb, " (%d) none of these.\n", len(k.Interests)+1)
	line("Notes", k.Notes)
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
