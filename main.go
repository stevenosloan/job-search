// jobscan: helpers for the climate-job-search skill.
//
//	jobscan climatebase-url [-sizes 101-250,251-500] [-sectors "A"] [-nonprofit] [-junior]
//	jobscan fetch-rr [flags]            (needs RR_API_KEY; see rr.go)
//	jobscan score [-config config.json] [-out ranked] postings.json [more.json ...]
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
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
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
	SalaryFloor  int      `json:"salary_floor"`
	SizeMin      int      `json:"size_min"`
	SizeMax      int      `json:"size_max"`
	ExcludeTerms []string `json:"exclude_terms"`
	MissionTerms []string `json:"mission_terms"`
	ProductTerms []string `json:"product_terms"`
	GrowthTerms  []string `json:"growth_terms"`
	StackTerms   []string `json:"stack_terms"`
	SeniorTerms  []string `json:"senior_terms"`
}

func defaultConfig() Config {
	return Config{
		SalaryFloor: 170000, SizeMin: 150, SizeMax: 500,
		ExcludeTerms: []string{"carbon credit", "carbon offset", "offset marketplace", "carbon marketplace", "credit marketplace", "carbon removal marketplace"},
		MissionTerms: []string{"land", "conservation", "forest", "soil", "agricultur", "wildlife", "watershed", "ecosystem", "restoration", "habitat", "regenerative", "biodiversity"},
		ProductTerms: []string{"product", "customer", "users", "roadmap", "product manager", "design"},
		GrowthTerms:  []string{"growth", "marketing", "acquisition", "seo", "experimentation", "funnel"},
		StackTerms:   []string{"typescript", "react", "node", "postgres", "aws", "performance", "observability", "playwright", "design system", "accessibility", "analytics"},
		SeniorTerms:  []string{"senior", "staff", "principal", "lead"},
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
	pts += min(len(m), 4) * 6
	if len(m) > 0 {
		why = append(why, "mission: "+strings.Join(first(m, 4), ", "))
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
	return Result{pts, j, strings.Join(why, "; "), strings.Join(flags, "; ")}, true, ""
}

func runScore(args []string) error {
	fs := flag.NewFlagSet("score", flag.ExitOnError)
	cfgPath := fs.String("config", "", "config.json overriding defaults")
	out := fs.String("out", "ranked", "output prefix")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: jobscan score [-config f] [-out prefix] postings.json [more.json ...]")
	}
	c := defaultConfig()
	if *cfgPath != "" {
		b, err := os.ReadFile(*cfgPath)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(b, &c); err != nil { // missing keys keep defaults
			return err
		}
	}
	var jobs []Job
	for _, path := range fs.Args() {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var part []Job
		if err := json.Unmarshal(b, &part); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		jobs = append(jobs, part...)
	}
	seen := map[string]bool{}
	var ranked []Result
	type drop struct {
		J   Job
		Why string
	}
	var dropped []drop
	for _, j := range jobs {
		key := strings.ToLower(j.Company) + "|" + strings.ToLower(j.Title)
		if seen[key] {
			continue
		}
		seen[key] = true
		if r, ok, why := score(j, c); ok {
			ranked = append(ranked, r)
		} else {
			dropped = append(dropped, drop{j, why})
		}
	}
	sort.SliceStable(ranked, func(a, b int) bool { return ranked[a].Score > ranked[b].Score })

	cf, err := os.Create(*out + ".csv")
	if err != nil {
		return err
	}
	defer cf.Close()
	w := csv.NewWriter(cf)
	w.Write([]string{"score", "title", "company", "size", "salary", "location", "source", "url", "why", "flags"})
	for _, r := range ranked {
		j := r.Job
		w.Write([]string{strconv.Itoa(r.Score), j.Title, j.Company, j.Size,
			rawStr(j.SalaryMin) + "-" + rawStr(j.SalaryMax),
			j.Location, j.Source, j.URL, r.Why, r.Flags})
	}
	w.Flush()

	var sb strings.Builder
	sb.WriteString("| # | Score | Role | Company | Why | Flags |\n|---|---|---|---|---|---|\n")
	for i, r := range ranked {
		t := r.Job.Title
		if r.Job.URL != "" {
			t = fmt.Sprintf("[%s](%s)", t, r.Job.URL)
		}
		fmt.Fprintf(&sb, "| %d | %d | %s | %s | %s | %s |\n", i+1, r.Score, t, r.Job.Company, r.Why, r.Flags)
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

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: jobscan climatebase-url|fetch-rr|score ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "climatebase-url":
		fs := flag.NewFlagSet("climatebase-url", flag.ExitOnError)
		sizes := fs.String("sizes", "101-250,251-500", "org size bands, comma-separated")
		sectors := fs.String("sectors", "", "sector names, | separated")
		np := fs.Bool("nonprofit", false, "nonprofits only")
		jr := fs.Bool("junior", false, "junior level")
		fs.Parse(os.Args[2:])
		fmt.Println(climatebaseURL(*sizes, *sectors, *np, *jr))
	case "fetch-rr":
		if err := runFetchRR(os.Args[2:]); err != nil {
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
