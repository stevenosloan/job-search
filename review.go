package main

// Second-pass review: `review-batch` writes the keyword top N for the
// posting-reviewer agent (.claude/agents/posting-reviewer.md), and
// `score -reviews` merges the agent's JSON back into the ranking.

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Review is one posting-reviewer verdict, matched to a posting by URL.
type Review struct {
	URL          string   `json:"url"`
	Company      string   `json:"company"`
	Title        string   `json:"title"`
	Fit          int      `json:"fit"`
	MissionTier  int      `json:"mission_tier"`
	RoleType     string   `json:"role_type"`
	StackOverlap string   `json:"stack_overlap"`
	LocationOK   *bool    `json:"location_ok"`
	PayOK        *bool    `json:"pay_ok"`
	Gaps         []string `json:"gaps"`
	Why          string   `json:"why"`
}

func (r *Review) note() string {
	s := r.Why
	if len(r.Gaps) > 0 {
		s += " Gaps: " + strings.Join(r.Gaps, "; ")
	}
	return strings.TrimSpace(s)
}

// loadReviews reads one or more reviewer files. A later file overrides an
// earlier one for the same URL. Out-of-range or URL-less entries are errors,
// since a silently wrong fit would reorder the list.
func loadReviews(paths []string) (map[string]Review, error) {
	out := map[string]Review{}
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, err
		}
		var rs []Review
		if err := json.Unmarshal(b, &rs); err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
		for i, r := range rs {
			if r.URL == "" {
				return nil, fmt.Errorf("%s: review %d has no url", p, i+1)
			}
			if r.Fit < 0 || r.Fit > 100 {
				return nil, fmt.Errorf("%s: review %d (%s) fit %d outside 0-100", p, i+1, r.URL, r.Fit)
			}
			out[r.URL] = r
		}
	}
	return out, nil
}

// applyReviews attaches reviews by URL and re-sorts: reviewed postings first
// by fit (keyword score breaks ties), then unreviewed ones by keyword score.
// A review whose location_ok is false adds a flag. Reviews that match no
// posting are reported on stderr.
func applyReviews(ranked []Result, reviews map[string]Review) []Result {
	used := map[string]bool{}
	for i := range ranked {
		rv, ok := reviews[ranked[i].Job.URL]
		if !ok {
			continue
		}
		used[rv.URL] = true
		ranked[i].Review = &rv
		if rv.LocationOK != nil && !*rv.LocationOK {
			ranked[i].Flags = joinFlags(ranked[i].Flags, "location doesn't work")
		}
	}
	var orphans []string
	for u := range reviews {
		if !used[u] {
			orphans = append(orphans, u)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		fmt.Fprintf(os.Stderr, "warning: %d review(s) matched no posting (deduped or not in inputs):\n  %s\n",
			len(orphans), strings.Join(orphans, "\n  "))
	}
	sort.SliceStable(ranked, func(a, b int) bool {
		ra, rb := ranked[a].Review, ranked[b].Review
		switch {
		case ra != nil && rb != nil:
			if ra.Fit != rb.Fit {
				return ra.Fit > rb.Fit
			}
			return ranked[a].Score > ranked[b].Score
		case ra != nil || rb != nil:
			return ra != nil
		default:
			return ranked[a].Score > ranked[b].Score
		}
	})
	return ranked
}

func joinFlags(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

// batchItem is what the reviewer reads: the posting plus its keyword score.
type batchItem struct {
	Job
	KeywordScore int `json:"keyword_score"`
}

func runReviewBatch(args []string) error {
	fs := flag.NewFlagSet("review-batch", flag.ExitOnError)
	cfgPath := fs.String("config", "", "YAML (or JSON) config; default config.yaml if present")
	top := fs.Int("top", 30, "how many of the keyword-ranked postings to include")
	maxDesc := fs.Int("max-desc", 6000, "truncate descriptions to this many characters (0 = no limit)")
	out := fs.String("out", "reviews/batch.json", "batch file to write")
	fs.Parse(args)
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: jobscan review-batch [-config f] [-top 30] [-max-desc 6000] [-out reviews/batch.json] postings.json [more.json ...]")
	}
	c, err := loadConfig(configPath(*cfgPath))
	if err != nil {
		return err
	}
	jobs, err := loadJobs(fs.Args())
	if err != nil {
		return err
	}
	ranked, _ := rankJobs(jobs, c)
	items := buildBatch(ranked, *top, *maxDesc)
	b, err := json.MarshalIndent(items, "", " ")
	if err != nil {
		return err
	}
	if dir := filepath.Dir(*out); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(*out, append(b, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Printf("%d postings -> %s\n", len(items), *out)
	return nil
}

func buildBatch(ranked []Result, top, maxDesc int) []batchItem {
	if top > 0 && top < len(ranked) {
		ranked = ranked[:top]
	}
	items := make([]batchItem, 0, len(ranked))
	for _, r := range ranked {
		j := r.Job
		if d := []rune(j.Description); maxDesc > 0 && len(d) > maxDesc {
			j.Description = string(d[:maxDesc]) + " [truncated]"
		}
		items = append(items, batchItem{j, r.Score})
	}
	return items
}

// missingFromBatch returns the batch items that have no review.
func missingFromBatch(items []batchItem, reviews map[string]Review) []batchItem {
	var miss []batchItem
	for _, it := range items {
		if _, ok := reviews[it.URL]; !ok {
			miss = append(miss, it)
		}
	}
	return miss
}

// missingPath turns reviews/batch.json into reviews/batch-missing.json.
func missingPath(batch string) string {
	ext := filepath.Ext(batch)
	return strings.TrimSuffix(batch, ext) + "-missing" + ext
}

// checkBatchCoverage warns about batch postings the reviewer skipped (it can
// report success while writing fewer objects, or alter a URL) and writes them
// to <batch>-missing.json for a re-run. A stale missing file is removed when
// coverage is complete.
func checkBatchCoverage(batch string, reviews map[string]Review) error {
	b, err := os.ReadFile(batch)
	if err != nil {
		return err
	}
	var items []batchItem
	if err := json.Unmarshal(b, &items); err != nil {
		return fmt.Errorf("%s: %w", batch, err)
	}
	miss := missingFromBatch(items, reviews)
	out := missingPath(batch)
	if len(miss) == 0 {
		if err := os.Remove(out); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Fprintf(os.Stderr, "reviews cover all %d postings in %s\n", len(items), batch)
		return nil
	}
	mb, err := json.MarshalIndent(miss, "", " ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, append(mb, '\n'), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "warning: %d of %d postings in %s have no review; re-run the reviewer on %s:\n",
		len(miss), len(items), batch, out)
	for _, m := range miss {
		fmt.Fprintf(os.Stderr, "  %s: %s (%s)\n", m.Company, m.Title, m.URL)
	}
	return nil
}
