// Package climatebase is the Climatebase board adapter (climatebase.org), a
// climate-sector job board. It sits behind a bot check, so Claude reads it in
// the user's browser; jobscan only builds the filtered listing URL.
package climatebase

import (
	"flag"
	"fmt"
	"net/url"
	"strings"

	"jobscan/boards"
)

// Config is boards.climatebase in the jobscan config.
type Config struct {
	Enabled   bool     `json:"enabled" yaml:"enabled"`
	Out       string   `json:"out" yaml:"out"`
	Sizes     []string `json:"sizes" yaml:"sizes"`
	Sectors   []string `json:"sectors" yaml:"sectors"`
	Nonprofit bool     `json:"nonprofit" yaml:"nonprofit"`
}

// Default is the board's built-in config.
func Default() Config {
	return Config{Enabled: false, Out: "postings-cb.json", Sizes: []string{"101-250", "251-500"}}
}

// Info summarizes the board for `jobscan boards`.
func (b Config) Info() boards.Info {
	return boards.Info{Name: "climatebase", Method: "browser (Claude)", Out: b.Out, Enabled: b.Enabled, Detail: "./jobscan climatebase-url"}
}

const cbCategories = "Software & IT:Back-end Engineering,DevOps,Front-end Engineering,Full-stack Engineering,Engineering Manager, Software,Software Architecture,Security Engineering"

// URL builds a filtered Climatebase listing URL.
func URL(sizes, sectors string, nonprofit, junior bool) string {
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

// Run is `jobscan climatebase-url`: flags default to cb and override it.
func Run(args []string, cb Config) error {
	fs := flag.NewFlagSet("climatebase-url", flag.ExitOnError)
	fs.String("config", "", "config file (defaults below come from boards.climatebase)")
	sizes := fs.String("sizes", strings.Join(cb.Sizes, ","), "org size bands, comma-separated")
	sectors := fs.String("sectors", strings.Join(cb.Sectors, "|"), "sector names, | separated")
	np := fs.Bool("nonprofit", cb.Nonprofit, "nonprofits only")
	jr := fs.Bool("junior", false, "junior level")
	fs.Parse(args)
	fmt.Println(URL(*sizes, *sectors, *np, *jr))
	return nil
}
