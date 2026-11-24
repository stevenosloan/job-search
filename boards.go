package main

// The board registry: which job boards a run collects from, and the
// postings files they produce. Each board's adapter is its own package under
// boards/<name>.

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"jobscan/boards"
	"jobscan/boards/browser"
	"jobscan/boards/climatebase"
	"jobscan/boards/remoterocketship"
)

// Boards is the config's boards section, one field per board adapter.
type Boards struct {
	Climatebase      climatebase.Config      `json:"climatebase" yaml:"climatebase"`
	RemoteRocketship remoterocketship.Config `json:"remote_rocketship" yaml:"remote_rocketship"`
	Browser          []browser.Board         `json:"browser" yaml:"browser"`
}

func defaultBoards() Boards {
	return Boards{Climatebase: climatebase.Default(), RemoteRocketship: remoterocketship.Default()}
}

// checkBoards validates each board and rejects two boards writing the same
// postings file.
func (c *Config) checkBoards() error {
	for i := range c.Boards.Browser {
		if err := c.Boards.Browser[i].Check(i); err != nil {
			return err
		}
	}
	outs := map[string]string{}
	for _, b := range c.boardList() {
		if prev, ok := outs[b.Out]; ok {
			return fmt.Errorf("boards %s and %s both write %s", prev, b.Name, b.Out)
		}
		outs[b.Out] = b.Name
	}
	return nil
}

func (c Config) boardList() []boards.Info {
	l := []boards.Info{c.Boards.Climatebase.Info(), c.Boards.RemoteRocketship.Info()}
	for _, b := range c.Boards.Browser {
		l = append(l, b.Info())
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

// runClimatebaseURL and runFetchRR load the config, then hand the board's
// section to its adapter as flag defaults.
func runClimatebaseURL(args []string) error {
	c, _, err := resolveConfig(configFlag(args))
	if err != nil {
		return err
	}
	if !c.Boards.Climatebase.Enabled {
		fmt.Fprintln(os.Stderr, "note: climatebase is disabled in the config")
	}
	return climatebase.Run(args, c.Boards.Climatebase)
}

func runFetchRR(args []string) error {
	c, _, err := resolveConfig(configFlag(args))
	if err != nil {
		return err
	}
	if !c.Boards.RemoteRocketship.Enabled {
		fmt.Fprintln(os.Stderr, "note: remote_rocketship is disabled in the config")
	}
	return remoterocketship.Run(args, c.Boards.RemoteRocketship)
}
