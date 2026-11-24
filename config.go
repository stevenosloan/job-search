package main

// The per-user config: where it lives, what it holds, and the candidate
// criteria rendered for the posting-reviewer.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Config struct {
	SalaryFloor  int      `json:"salary_floor" yaml:"salary_floor"`
	SizeMin      int      `json:"size_min" yaml:"size_min"`
	SizeMax      int      `json:"size_max" yaml:"size_max"`
	ExcludeTerms []string `json:"exclude_terms" yaml:"exclude_terms"`
	MissionTerms []string `json:"mission_terms" yaml:"mission_terms"`
	ProductTerms []string `json:"product_terms" yaml:"product_terms"`
	GrowthTerms  []string `json:"growth_terms" yaml:"growth_terms"`
	StackTerms   []string `json:"stack_terms" yaml:"stack_terms"`
	SeniorTerms  []string `json:"senior_terms" yaml:"senior_terms"`
	// MissionTerms describe the companies or missions the user cares about.
	// MissionWeight is points per mission term hit (up to 4 hits).
	MissionWeight int `json:"mission_weight" yaml:"mission_weight"`
	// NoMissionPenalty is subtracted when a posting matches no mission term.
	NoMissionPenalty int `json:"no_mission_penalty" yaml:"no_mission_penalty"`
	// Candidate is the profile the posting-reviewer judges against.
	Candidate Candidate `json:"candidate" yaml:"candidate"`
	// Boards picks the job boards to search and their filters.
	Boards Boards `json:"boards" yaml:"boards"`
}

func defaultConfig() Config {
	return Config{
		SalaryFloor: 170000, SizeMin: 150, SizeMax: 500,
		ProductTerms:  []string{"product", "customer", "users", "roadmap", "product manager", "design"},
		GrowthTerms:   []string{"growth", "marketing", "acquisition", "seo", "experimentation", "funnel"},
		StackTerms:    []string{"typescript", "react", "node", "postgres", "aws", "performance", "observability", "playwright", "design system", "accessibility", "analytics"},
		SeniorTerms:   []string{"senior", "staff", "principal", "lead"},
		MissionWeight: 6, NoMissionPenalty: 0,
		Boards: defaultBoards(),
	}
}

// loadConfig reads a YAML config (JSON also parses, as a YAML subset) over
// the defaults. Missing keys keep defaults; unknown keys are an error so a
// typo cannot silently fall back to a default. An empty path means defaults.
func loadConfig(path string) (Config, error) {
	c := defaultConfig()
	if path == "" {
		return c, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil && err != io.EOF {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.checkBoards(); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
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
