// jobscan: helpers for the job-search skill.
//
//	jobscan boards [-files]             (boards in the config and their postings files)
//	jobscan climatebase-url [-sizes 101-250,251-500] [-sectors "A"] [-nonprofit] [-junior]
//	jobscan fetch-rr [flags]            (needs RR_API_KEY; see rr.go)
//	jobscan review-batch [-top 30] [-out reviews/batch.json] [postings.json ...]
//	jobscan score [-config f] [-out ranked] [-reviews r.json] [postings.json ...]
//
// Every command reads the per-user config (see configPath in boards.go);
// flag defaults for climatebase-url and fetch-rr come from its boards section.
// Without postings files, score and review-batch use the enabled boards' files.
//
// postings.json: array of {title, company, location, remote, salary_min,
// salary_max, size, url, source, description}; any field may be missing.
// Writes <out>.md and <out>.csv.
package main

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Job struct {
	Title       string          `json:"title"`
	Company     string          `json:"company"`
	Location    string          `json:"location"`
	Remote      any             `json:"remote"`
	SalaryMin   json.RawMessage `json:"salary_min"`
	SalaryMax   json.RawMessage `json:"salary_max"`
	Size        string          `json:"size"`
	URL         string          `json:"url"`
	Source      string          `json:"source"`
	Description string          `json:"description"`
}

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

const cbCategories = "Software & IT:Back-end Engineering,DevOps,Front-end Engineering,Full-stack Engineering,Engineering Manager, Software,Software Architecture,Security Engineering"

func climatebaseURL(sizes, sectors string, nonprofit, junior bool) string {
	level := "Senior Level"
	if junior {
		level = "Junior Level"
	}
	p := []string{"experience_levels=" + url.QueryEscape(level), "categories=" + url.QueryEscape(cbCategories)}
	if sectors != "" {
		p = append(p, "sectors="+url.QueryEscape(sectors))
	}
	if sizes != "" {
		p = append(p, "organization_size="+url.QueryEscape(strings.ReplaceAll(sizes, ",", "|")))
	}
	if nonprofit {
		p = append(p, "organization_type=Nonprofit")
	}
	return "https://climatebase.org/jobs?" + strings.Join(p, "&")
}

var numRe = regexp.MustCompile(`\d[\d,]*`)

// sizeRange parses "101-250", "501-1000", "5000+", "~100".
func sizeRange(s string) (lo, hi int, ok bool) {
	m := numRe.FindAllString(s, -1)
	if len(m) == 0 {
		return 0, 0, false
	}
	lo, _ = strconv.Atoi(strings.ReplaceAll(m[0], ",", ""))
	switch {
	case len(m) > 1:
		hi, _ = strconv.Atoi(strings.ReplaceAll(m[1], ",", ""))
	case strings.Contains(s, "+"):
		hi = 1 << 30
	default:
		hi = lo
	}
	return lo, hi, true
}

var moneyRe = regexp.MustCompile(`(?i)\$?\s*(\d[\d,.]*)\s*(k)?`)

// money accepts 165000, "165000", "$165,000", "$165k", "165".
func money(raw json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" || s == "null" {
		return 0, false
	}
	m := moneyRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(strings.ReplaceAll(m[1], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	if m[2] != "" || n < 1000 {
		n *= 1000
	}
	return int(n), true
}

func rawStr(raw json.RawMessage) string {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "null" {
		return ""
	}
	return s
}

func hits(text string, terms []string) []string {
	var out []string
	for _, t := range terms {
		if regexp.MustCompile(`\b` + regexp.QuoteMeta(t)).MatchString(text) {
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func first(xs []string, n int) []string {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

type Result struct {
	Score      int
	Job        Job
	Why, Flags string
	Review     *Review // set when a posting-reviewer review matched by URL
}

// score returns ok=false (with reason in why) when the job is excluded.
func score(j Job, c Config) (r Result, ok bool, reason string) {
	text := strings.ToLower(j.Title + " " + j.Company + " " + j.Description)
	if bad := hits(text, c.ExcludeTerms); len(bad) > 0 {
		return r, false, "excluded: " + strings.Join(bad, ", ")
	}
	var why, flags []string
	pts := 0
	title := strings.ToLower(j.Title)
	for _, t := range c.SeniorTerms {
		if strings.Contains(title, t) {
			pts += 10
			break
		}
	}
	lo, okLo := money(j.SalaryMin)
	hi, okHi := money(j.SalaryMax)
	top, haveTop := hi, okHi
	if !haveTop {
		top, haveTop = lo, okLo
	}
	switch {
	case !haveTop:
		flags = append(flags, "salary not posted")
	case top >= c.SalaryFloor:
		pts += 20
		why = append(why, fmt.Sprintf("pay up to $%d", top))
	default:
		pts -= 15
		flags = append(flags, fmt.Sprintf("pay tops out $%d (< floor)", top))
	}
	if sLo, sHi, ok := sizeRange(j.Size); !ok {
		flags = append(flags, "size unknown")
	} else if sLo <= c.SizeMax && sHi >= c.SizeMin {
		pts += 15
		why = append(why, "size "+j.Size)
	} else {
		pts -= 10
		flags = append(flags, "size "+j.Size+" outside range")
	}
	m := hits(text, c.MissionTerms)
	pts += min(len(m), 4) * c.MissionWeight
	if len(m) > 0 {
		why = append(why, "mission: "+strings.Join(first(m, 4), ", "))
	} else if c.NoMissionPenalty > 0 {
		pts -= c.NoMissionPenalty
		flags = append(flags, "no mission match")
	}
	pts += min(len(hits(text, c.ProductTerms)), 3) * 3
	if g := hits(text, c.GrowthTerms); len(g) > 0 {
		pts -= min(len(g), 3) * 3
		flags = append(flags, "growth-flavored: "+strings.Join(first(g, 3), ", "))
	}
	s := hits(text, c.StackTerms)
	pts += min(len(s), 5) * 2
	if len(s) > 0 {
		why = append(why, "stack: "+strings.Join(first(s, 5), ", "))
	}
	return Result{Score: pts, Job: j, Why: strings.Join(why, "; "), Flags: strings.Join(flags, "; ")}, true, ""
}

// companyKey normalizes a company name for dedupe, so "Afresh" and
// "Afresh Technologies, Inc." collapse to the same key.
func companyKey(name string) string {
	k := strings.ToLower(strings.TrimSpace(name))
	k = strings.NewReplacer(",", " ", ".", " ").Replace(k)
	words := strings.Fields(k)
	suffix := map[string]bool{"inc": true, "llc": true, "ltd": true, "corp": true, "corporation": true,
		"co": true, "company": true, "technologies": true, "technology": true, "gmbh": true, "pbc": true}
	for len(words) > 1 && suffix[words[len(words)-1]] {
		words = words[:len(words)-1]
	}
	return strings.Join(words, " ")
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

func loadJobs(paths []string) ([]Job, error) {
	var jobs []Job
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var part []Job
		if err := json.Unmarshal(b, &part); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		jobs = append(jobs, part...)
	}
	return jobs, nil
}

type droppedJob struct {
	J   Job
	Why string
}

// rankJobs dedupes on normalized company + title (first file wins), drops
// exclusions, and sorts by keyword score.
func rankJobs(jobs []Job, c Config) ([]Result, []droppedJob) {
	seen := map[string]bool{}
	var ranked []Result
	var drops []droppedJob
	for _, j := range jobs {
		key := companyKey(j.Company) + "|" + strings.ToLower(strings.TrimSpace(j.Title))
		if seen[key] {
			continue
		}
		seen[key] = true
		if r, ok, why := score(j, c); ok {
			ranked = append(ranked, r)
		} else {
			drops = append(drops, droppedJob{j, why})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].Score > ranked[b].Score })
	return ranked, drops
}

// mdCell keeps a value from breaking a Markdown table row.
func mdCell(s string) string {
	return strings.NewReplacer("|", "/", "\n", " ").Replace(s)
}

func runScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	cfgPath := fs.String("config", "", "YAML (or JSON) config; default $JOBSCAN_CONFIG, ./config.yaml, then ~/.config/jobscan/config.yaml")
	out := fs.String("out", "ranked", "output prefix")
	reviewPaths := fs.String("reviews", "", "comma-separated posting-reviewer JSON files to merge")
	batchPath := fs.String("batch", "", "review-batch file the reviews were made from; postings in it without a review are listed and written to <batch>-missing.json")
	fs.Parse(args)
	c, _, err := resolveConfig(*cfgPath)
	if err != nil {
		return err
	}
	files, err := postingArgs(fs, c)
	if err != nil {
		return fmt.Errorf("%w\nusage: jobscan score [-config f] [-out prefix] [-reviews a.json,b.json [-batch reviews/batch.json]] [postings.json ...]", err)
	}
	jobs, err := loadJobs(files)
	if err != nil {
		return err
	}
	ranked, dropped := rankJobs(jobs, c)
	if *reviewPaths != "" {
		reviews, err := loadReviews(strings.Split(*reviewPaths, ","))
		if err != nil {
			return err
		}
		ranked = applyReviews(ranked, reviews)
		if *batchPath != "" {
			if err := checkBatchCoverage(*batchPath, reviews); err != nil {
				return err
			}
		}
	} else if *batchPath != "" {
		return fmt.Errorf("-batch needs -reviews")
	}

	cf, err := os.Create(*out + ".csv")
	if err != nil {
		return err
	}
	defer cf.Close()
	w := csv.NewWriter(cf)
	w.Write([]string{"fit", "score", "title", "company", "size", "salary", "location", "source", "url", "why", "flags", "mission_tier", "role_type", "review"})
	for _, r := range ranked {
		j := r.Job
		fit, tier, role, note := "", "", "", ""
		if rv := r.Review; rv != nil {
			fit, tier, role, note = strconv.Itoa(rv.Fit), strconv.Itoa(rv.MissionTier), rv.RoleType, rv.note()
		}
		w.Write([]string{fit, strconv.Itoa(r.Score), j.Title, j.Company, j.Size,
			rawStr(j.SalaryMin) + "-" + rawStr(j.SalaryMax),
			j.Location, j.Source, j.URL, r.Why, r.Flags, tier, role, note})
	}
	w.Flush()

	var sb strings.Builder
	sb.WriteString("| # | Fit | Score | Role | Company | Why | Flags |\n|---|---|---|---|---|---|---|\n")
	for i, r := range ranked {
		t := r.Job.Title
		if r.Job.URL != "" {
			t = fmt.Sprintf("[%s](%s)", t, r.Job.URL)
		}
		fit, why := "", r.Why
		if rv := r.Review; rv != nil {
			fit, why = strconv.Itoa(rv.Fit), rv.note()
		}
		fmt.Fprintf(&sb, "| %d | %s | %d | %s | %s | %s | %s |\n", i+1, fit, r.Score, t, r.Job.Company, mdCell(why), mdCell(r.Flags))
	}
	if len(dropped) > 0 {
		sb.WriteString("\nExcluded:\n")
		for _, d := range dropped {
			fmt.Fprintf(&sb, "- %s: %s (%s)\n", d.J.Company, d.J.Title, d.Why)
		}
	}
	if err := os.WriteFile(*out+".md", []byte(sb.String()), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d ranked, %d excluded -> %s.md / %s.csv\n", len(ranked), len(dropped), *out, *out)
	return nil
}

func runClimatebaseURL(args []string) error {
	c, _, err := resolveConfig(configFlag(args))
	if err != nil {
		return err
	}
	cb := c.Boards.Climatebase
	if !cb.Enabled {
		fmt.Fprintln(os.Stderr, "note: climatebase is disabled in the config")
	}
	fs := flag.NewFlagSet("climatebase-url", flag.ExitOnError)
	fs.String("config", "", "config file (defaults below come from boards.climatebase)")
	sizes := fs.String("sizes", strings.Join(cb.Sizes, ","), "org size bands, comma-separated")
	sectors := fs.String("sectors", strings.Join(cb.Sectors, "|"), "sector names, | separated")
	np := fs.Bool("nonprofit", cb.Nonprofit, "nonprofits only")
	jr := fs.Bool("junior", false, "junior level")
	fs.Parse(args)
	fmt.Println(climatebaseURL(*sizes, *sectors, *np, *jr))
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jobscan boards|climatebase-url|fetch-rr|review-batch|score ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "climatebase-url":
		if err := runClimatebaseURL(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "boards":
		if err := runBoards(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "fetch-rr":
		if err := runFetchRR(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "review-batch":
		if err := runReviewBatch(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "score":
		if err := runScore(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command:", os.Args[1])
		os.Exit(2)
	}
}
