// Package browser is the adapter for other job boards: any other job board, listed in the config by URL, that
// Claude reads in the user's browser and records by hand.
package browser

import (
	"fmt"

	"jobscan/boards"
)

// Board is one entry in boards.browser in the jobscan config.
type Board struct {
	Name    string `json:"name" yaml:"name"`
	Enabled bool   `json:"enabled" yaml:"enabled"`
	URL     string `json:"url" yaml:"url"`
	Out     string `json:"out" yaml:"out"`
	Notes   string `json:"notes" yaml:"notes"`
}

// Check rejects an entry that can't be collected and fills its default
// postings file name.
func (b *Board) Check(i int) error {
	if b.Name == "" || b.URL == "" {
		return fmt.Errorf("boards.browser[%d] needs a name and url", i)
	}
	if b.Out == "" {
		b.Out = "postings-" + b.Name + ".json"
	}
	return nil
}

// Info summarizes the board for `jobscan boards`.
func (b Board) Info() boards.Info {
	d := b.URL
	if b.Notes != "" {
		d += " (" + b.Notes + ")"
	}
	return boards.Info{Name: b.Name, Method: "browser (Claude)", Out: b.Out, Enabled: b.Enabled, Detail: d}
}
