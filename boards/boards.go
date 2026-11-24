// Package boards holds what every job board adapter shares: the postings
// format they write and the summary each reports about itself.
package boards

import (
	"encoding/json"
	"strings"
)

// Job is one posting in jobscan's postings format (postings-*.json). Any
// field may be missing; salary may be a number or a string like "$165k".
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

// Info is what an adapter reports for `jobscan boards` and for picking the
// default postings files.
type Info struct {
	Name, Method, Out string
	Enabled           bool
	Detail            string
}

// SplitList splits a comma-separated flag value, dropping empty items.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
