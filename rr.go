package main

// Remote Rocketship API client (POST /api/openclaw/jobs/).
// Spec: https://www.remoterocketship.com/openapi.json
// Quota (standard plan): 500 requests or 3,000 jobs per UTC day, whichever first.
// The key is read from the RR_API_KEY environment variable, or, if that is unset,
// from RR_API_KEY in a .env file (-env-file). It is never accepted as a flag,
// logged, or written by this tool.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type rrJob struct {
	ID               flexID  `json:"id"`
	RoleTitle        string  `json:"roleTitle"`
	URL              string  `json:"url"`
	Slug             string  `json:"slug"`
	RoleDescription  *string `json:"roleDescription"`
	RoleRequirements *string `json:"roleRequirements"`
	Company          *struct {
		ID   flexID `json:"id"`
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"company"`
	SalaryRange json.RawMessage `json:"salaryRange"`
}

// flexID accepts a JSON number or string; the live API sends numeric ids
// where the spec implies strings.
type flexID string

func (f *flexID) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		*f = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = flexID(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("id: %w", err)
	}
	*f = flexID(n.String())
	return nil
}

func (f flexID) String() string { return string(f) }

type rrResponse struct {
	JobOpenings []rrJob `json:"jobOpenings"`
	TotalCount  int     `json:"totalCount"`
	Pagination  struct {
		Page        int  `json:"page"`
		TotalPages  int  `json:"totalPages"`
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pagination"`
}

type rrError struct {
	Message, Code, Hint string
}

// loadKeyFromDotEnv sets RR_API_KEY from a dotenv file when the environment lacks
// it. Only that one key is read. A missing file is not an error. Errors never
// include file contents.
func loadKeyFromDotEnv(path string) error {
	if os.Getenv("RR_API_KEY") != "" || path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		fmt.Fprintf(os.Stderr, "warning: %s is readable by other users; run: chmod 600 %s\n", path, path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "RR_API_KEY" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if v != "" {
			return os.Setenv("RR_API_KEY", v)
		}
	}
	return nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var salaryTok = regexp.MustCompile(`(?i)\d[\d,]*\.?\d*\s*k?`)

// parseAmount reads 120000, "120000", "$120,000", "120k". ok=false for anything else.
func parseAmount(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, x > 0
	case string:
		t := strings.ToLower(strings.TrimSpace(salaryTok.FindString(x)))
		mult := 1.0
		if strings.HasSuffix(t, "k") {
			mult, t = 1000, strings.TrimSpace(strings.TrimSuffix(t, "k"))
		}
		n, err := strconv.ParseFloat(strings.ReplaceAll(t, ",", ""), 64)
		return n * mult, err == nil && n > 0
	}
	return 0, false
}

func fmtAmount(n float64) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(int(n + 0.5))
}

// salaryFromRaw maps salaryRange to annual USD bounds. The spec types it as
// object|null with no defined fields, so this reads keys by name: *min*/*from*/
// *low* and *max*/*to*/*high* (one level of nesting allowed), a period-like key
// (hour/week/month scale to annual), and a currency-like key (non-USD -> unknown,
// since unknown pay is not a fail but a wrong figure is). A bare string falls
// back to the first two amounts in it.
func salaryFromRaw(raw json.RawMessage) (lo, hi string) {
	var v any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return "", ""
	}
	if s, ok := v.(string); ok {
		var nums []float64
		for _, t := range salaryTok.FindAllString(s, -1) {
			if n, ok := parseAmount(t); ok {
				nums = append(nums, n)
			}
		}
		switch len(nums) {
		case 0:
			return "", ""
		case 1:
			return fmtAmount(nums[0]), ""
		}
		a, b := min(nums[0], nums[1]), max(nums[0], nums[1])
		return fmtAmount(a), fmtAmount(b)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "", ""
	}
	var a, b, usdA, usdB float64
	period, currency := "", ""
	var walk func(m map[string]any)
	walk = func(m map[string]any) {
		for k, val := range m {
			key := strings.ToLower(k)
			if sub, ok := val.(map[string]any); ok {
				walk(sub)
				continue
			}
			if s, ok := val.(string); ok {
				switch {
				case strings.Contains(key, "currency"):
					// a symbol like "$" is ambiguous (CAD, AUD); only use it when no code is given
					if !strings.Contains(key, "symbol") || currency == "" {
						currency = strings.ToUpper(s)
					}
					continue
				case strings.Contains(key, "period") || strings.Contains(key, "interval") ||
					strings.Contains(key, "unit") || strings.Contains(key, "frequency") || key == "per" ||
					strings.HasSuffix(key, "type"):
					period = strings.ToLower(s)
					continue
				}
			}
			n, ok := parseAmount(val)
			if !ok {
				continue
			}
			switch {
			case strings.Contains(key, "asusd") && strings.Contains(key, "min"):
				usdA = n
			case strings.Contains(key, "asusd") && strings.Contains(key, "max"):
				usdB = n
			case strings.Contains(key, "min") || strings.Contains(key, "from") || strings.Contains(key, "low"):
				a = n
			case strings.Contains(key, "max") || key == "to" || strings.HasSuffix(key, "to") || strings.Contains(key, "high"):
				b = n
			}
		}
	}
	walk(obj)
	if usdA > 0 || usdB > 0 {
		// the API's own USD conversion; same pay period as the native amounts
		a, b = usdA, usdB
	} else if currency != "" && currency != "USD" && currency != "$" {
		return "", ""
	}
	mult := 1.0
	switch {
	case strings.Contains(period, "hour"):
		mult = 2080
	case strings.Contains(period, "week"):
		mult = 52
	case strings.Contains(period, "month"):
		mult = 12
	}
	a, b = a*mult, b*mult
	if a > 0 && b > 0 && a > b {
		a, b = b, a
	}
	if a == 0 && b > 0 {
		return fmtAmount(b), ""
	}
	return fmtAmount(a), fmtAmount(b)
}

// salaryShape summarizes a salaryRange value's structure (e.g. "{currency,max,min}")
// so a live run can report what the API actually sends.
func salaryShape(raw json.RawMessage) string {
	var v any
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return "null"
	}
	m, ok := v.(map[string]any)
	if !ok {
		return fmt.Sprintf("%T", v)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return "{" + strings.Join(keys, ",") + "}"
}

func toJob(j rrJob, sizeLabel string) Job {
	desc := ""
	if j.RoleDescription != nil {
		desc += *j.RoleDescription
	}
	if j.RoleRequirements != nil {
		desc += "\n" + *j.RoleRequirements
	}
	lo, hi := salaryFromRaw(j.SalaryRange)
	job := Job{Title: j.RoleTitle, URL: j.URL, Source: "remoterocketship", Size: sizeLabel,
		Description: desc, Remote: true}
	if j.Company != nil {
		job.Company = j.Company.Name
	}
	if lo != "" {
		job.SalaryMin = json.RawMessage(strconv.Quote(lo))
	}
	if hi != "" {
		job.SalaryMax = json.RawMessage(strconv.Quote(hi))
	}
	return job
}

// bandLabel turns the API's "51,200" into "51-200" (the comma would read as a thousands separator).
func bandLabel(b string) string {
	if strings.HasSuffix(b, ",") {
		return strings.TrimSuffix(b, ",") + "+"
	}
	return strings.ReplaceAll(b, ",", "-")
}

func runFetchRR(args []string) error {
	c, _, err := resolveConfig(configFlag(args))
	if err != nil {
		return err
	}
	rb := c.Boards.RemoteRocketship
	if !rb.Enabled {
		fmt.Fprintln(os.Stderr, "note: remote_rocketship is disabled in the config")
	}
	j := func(l []string) string { return strings.Join(l, ",") }
	fs := flag.NewFlagSet("fetch-rr", flag.ExitOnError)
	fs.String("config", "", "config file (defaults below come from boards.remote_rocketship)")
	titles := fs.String("titles", j(rb.Titles), "jobTitleFilters, comma-separated")
	locations := fs.String("locations", j(rb.Locations), "locationFilters")
	seniority := fs.String("seniority", j(rb.Seniority), "entry-level,junior,mid,senior,expert")
	bands := fs.String("sizes", strings.Join(rb.Sizes, ";"), "companySizeFilters bands separated by ';' (each band is its own request series)")
	keywords := fs.String("keywords", j(rb.Keywords), "required keywords (AND)")
	exclude := fs.String("exclude", j(rb.Exclude), "excludedKeywordFilters")
	tech := fs.String("tech", j(rb.Tech), "techStackFilters, e.g. TypeScript,React")
	industries := fs.String("industries", j(rb.Industries), "industriesFilters")
	minSalary := fs.Int("min-salary", rb.MinSalary, "minSalaryFilter (annual USD); jobs without salary still returned")
	maxPages := fs.Int("max-pages", rb.MaxPages, "page cap per size band (50 jobs/page)")
	maxJobs := fs.Int("max-jobs", rb.MaxJobs, "stop after this many jobs total (daily quota is 3,000)")
	out := fs.String("out", rb.Out, "output JSON in jobscan postings format")
	sandbox := fs.Bool("sandbox", false, "POST to the keyless sandbox and print the raw response, to inspect field shapes")
	envFile := fs.String("env-file", ".env", "dotenv file to read RR_API_KEY from when it is not in the environment (\"\" to disable)")
	fs.Parse(args)

	base := os.Getenv("RR_BASE_URL")
	if base == "" {
		base = "https://www.remoterocketship.com"
	}
	client := &http.Client{Timeout: 45 * time.Second}

	post := func(path string, body any, auth bool) (*http.Response, []byte, error) {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest("POST", base+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("RR-API-Version", "1")
		if auth {
			req.Header.Set("Authorization", "Bearer "+os.Getenv("RR_API_KEY"))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		return resp, data, err
	}

	if *sandbox {
		resp, data, err := post("/api/openclaw/sandbox/", map[string]any{"filters": map[string]any{}}, false)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusOK {
			fmt.Fprintf(os.Stderr, "sandbox returned HTTP %d\n", resp.StatusCode)
		}
		var pretty bytes.Buffer
		if json.Indent(&pretty, data, "", "  ") != nil {
			pretty.Write(data)
		}
		fmt.Println(pretty.String())
		return nil
	}
	if err := loadKeyFromDotEnv(*envFile); err != nil {
		return fmt.Errorf("reading %s: %w", *envFile, err)
	}
	if os.Getenv("RR_API_KEY") == "" {
		return fmt.Errorf("set RR_API_KEY in the environment or in %s (from your Remote Rocketship account page)", *envFile)
	}

	var all []Job
	seen := map[string]bool{}
	requests, jobsReturned := 0, 0
	remaining := -1                    // X-JobLimit-Remaining, when the server reports it
	shapes := map[string]int{}         // salaryRange structure -> count
	shapeSample := map[string]string{} // one raw example per structure (printed, never stored)

	for _, band := range strings.Split(*bands, ";") {
		band = strings.TrimSpace(band)
		for page := 1; page <= *maxPages; page++ {
			if len(all) >= *maxJobs {
				break
			}
			if remaining >= 0 && remaining < 50 {
				fmt.Fprintf(os.Stderr, "stopping: only %d jobs of quota left today\n", remaining)
				goto done
			}
			filters := map[string]any{
				"page": page, "itemsPerPage": 50, "sortBy": "DateAdded",
				"jobTitleFilters": splitList(*titles), "locationFilters": splitList(*locations),
				"seniorityFilters": splitList(*seniority), "employmentTypeFilters": []string{"full-time"},
				"hideGhostJobsFilter": "true", "showRemoteJobs": true,
				"minSalaryFilter": *minSalary, "showJobsWithoutSalaryWithMinSalaryFilter": true,
			}
			if band != "" {
				filters["companySizeFilters"] = []string{band}
			}
			for k, v := range map[string]string{"keywordFilters": *keywords, "excludedKeywordFilters": *exclude,
				"techStackFilters": *tech, "industriesFilters": *industries} {
				if l := splitList(v); len(l) > 0 {
					filters[k] = l
				}
			}
			resp, data, err := post("/api/openclaw/jobs/", map[string]any{"filters": filters, "includeJobDescription": true}, true)
			if err != nil {
				return err
			}
			requests++
			if v := resp.Header.Get("X-JobLimit-Remaining"); v != "" {
				if n, err := strconv.Atoi(v); err == nil {
					remaining = n
				}
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				fmt.Fprintf(os.Stderr, "rate limited (429); Retry-After=%ss. Keeping %d jobs fetched so far.\n", resp.Header.Get("Retry-After"), len(all))
				goto done
			}
			if resp.StatusCode != http.StatusOK {
				var e rrError
				json.Unmarshal(data, &e)
				return fmt.Errorf("HTTP %d: %s %s (%s)", resp.StatusCode, e.Code, e.Message, e.Hint)
			}
			var r rrResponse
			if err := json.Unmarshal(data, &r); err != nil {
				return fmt.Errorf("decode response: %w", err)
			}
			jobsReturned += len(r.JobOpenings)
			for _, j := range r.JobOpenings {
				id := j.ID.String()
				if seen[id] {
					continue
				}
				seen[id] = true
				sh := salaryShape(j.SalaryRange)
				if shapes[sh]++; shapes[sh] == 1 && sh != "null" {
					shapeSample[sh] = string(j.SalaryRange)
				}
				all = append(all, toJob(j, bandLabel(band)))
			}
			if !r.Pagination.HasNextPage {
				break
			}
		}
	}
done:
	b, _ := json.MarshalIndent(all, "", "  ")
	if err := os.WriteFile(*out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("%d jobs written to %s (%d requests, %d jobs returned", len(all), *out, requests, jobsReturned)
	if remaining >= 0 {
		fmt.Printf(", %d job-quota left today", remaining)
	}
	fmt.Println(")")
	if len(shapes) > 0 {
		fmt.Fprintln(os.Stderr, "salaryRange shapes seen:")
		for sh, n := range shapes {
			fmt.Fprintf(os.Stderr, "  %4d  %s  %s\n", n, sh, shapeSample[sh])
		}
	}
	return nil
}
