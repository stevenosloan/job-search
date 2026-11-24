# jobscan: job search

Finds software engineering jobs and ranks them against your resume and the criteria you set: the roles you want, your location, pay and company size, what you won't work on, and the missions or industries you care about.

Most of the work happens in a Claude Code skill, `/job-search`. It collects postings from the job boards you configure, then runs `jobscan`, a small Go CLI in this repo, to score them with keywords. A Haiku subagent then reviews the shortlist, and the skill writes a ranked report.

## How it works

1. **Collect.** Postings are saved as JSON files named `postings-*.json`, one file per board.
   - **Browser boards:** many boards sit behind bot checks or need you to be signed in, so Claude reads them in your own Chrome through the Claude in Chrome extension.
   - **Remote Rocketship (optional):** an API with your own key. You run the fetch yourself.
2. **Score.** `jobscan score` removes duplicates, drops postings matching your exclusions, and scores each posting on:
   - seniority
   - pay against your floor
   - company size
   - mission terms (the companies or industries you're interested in)
   - product vs. growth focus
   - your stack
3. **Review.** `jobscan review-batch` writes the top postings, plus a criteria file built from your config, for the `posting-reviewer` agent (Haiku). The agent gives each posting a fit score, and `jobscan score -reviews` merges those scores back into the ranking.
4. **Report.** Claude checks the top results against the actual postings and gives you a short ranked list with pay, size, gaps and links.

## Requirements

- **[Claude Code](https://claude.com/claude-code).** Run the skill from Claude Code, either the CLI or the desktop app, opened in this folder. The skill, the reviewer agent and the browser steps all depend on it.
- **Chrome with the Claude in Chrome extension.**
  - This is needed for any board read in a browser, which is most of them.
  - Install the [Claude extension from the Chrome Web Store](https://chromewebstore.google.com/detail/claude/fcoeoabgfenejglbffodgkkbkcdhcgfn), sign in to it with your Claude account, and connect it to Claude Code. Claude Code's Chrome docs explain how; in the CLI, `/chrome` shows the connection status.
  - Before a run, sign in to any boards that need an account.
  - If a board shows a CAPTCHA or "verify you're human" page, Claude stops and asks you to complete it. It won't complete or bypass the check itself.
- **Go 1.21 or later** to build `jobscan`.
- **A Remote Rocketship API key** (optional), only if you enable that board.

## Setup

```bash
go test ./... && go build -o jobscan .
```

```bash
cp config.example.yaml config.yaml
```

Edit `config.yaml` (see [Configuration](#configuration)). You can keep it at `~/.config/jobscan/config.yaml` instead, or point `JOBSCAN_CONFIG` at any path.

Put your resume in the repo root as `resume.md`. It's gitignored, and the skill uses it to tune `stack_terms` and to judge fit.

If you use Remote Rocketship, create your key file:

```bash
cp .env.example .env && chmod 600 .env
```

Then add your key after `RR_API_KEY=` in your own editor. `.env` is gitignored, and jobscan reads only that one key from it. Don't paste the key into a Claude conversation.

## Running a search

Open Claude Code in this folder, make sure Chrome and the extension are connected, and run:

```
/job-search
```

What happens during a run:

1. Claude reads your config (or helps you fill one in from the template), confirms your criteria, and lists the boards with `./jobscan boards`.
2. For each browser board that's turned on, Claude opens a new Chrome tab, reads the listings, and records postings in that board's `out` file.
3. If Remote Rocketship is turned on, Claude asks you to run `./jobscan fetch-rr` yourself. It has a daily quota of 500 requests or 3,000 jobs.
4. Claude scores the postings, runs the Haiku review, re-runs the reviewer on any postings it skipped, and reports the results.

The results go to `ranked.md` and `ranked.csv`.

## Configuration

Your config is personal and isn't committed. `config.example.yaml` is the template. jobscan looks for a config in this order:

1. `-config <file>`
2. `$JOBSCAN_CONFIG`
3. `./config.yaml`
4. `~/.config/jobscan/config.yaml`, or the same path under `$XDG_CONFIG_HOME`

If it finds none, it uses built-in defaults and prints a note. The defaults have no exclusions or mission terms; your criteria come from your config. Missing keys keep their defaults. Unknown keys are an error, so a typo can't fall back to a default without you noticing.

| Section | What it controls |
|---|---|
| `candidate` | Who the reviewer judges for: roles, strengths, areas that aren't a fit, location, and mission interests in priority order. |
| `salary_floor`, `size_min`, `size_max` | Pay floor and preferred company size. |
| `exclude_terms` | Postings matching any of these are dropped: industries, business models or companies you won't work for. |
| `mission_terms`, `stack_terms`, `product_terms`, `growth_terms`, `senior_terms` | Keyword lists for scoring. These match the start of a word, so `soil` also matches "soils". Prefer full phrases over short fragments that turn up in unrelated text. |
| `mission_weight`, `no_mission_penalty` | Points per mission term matched, and points taken off postings with no mission match. |
| `boards` | Which job boards to search, and how. |

### Job boards

```yaml
boards:
  remote_rocketship:    # remote-jobs API; you run ./jobscan fetch-rr
    enabled: true
    out: postings-rr.json
    titles: ["Software Engineer"]
    # locations, seniority, sizes, keywords, exclude, tech, industries,
    # min_salary, max_pages, max_jobs
  climatebase:          # climate-sector board; Claude reads it in Chrome
    enabled: false
    out: postings-cb.json
    sizes: ["101-250", "251-500"]
    sectors: []
  browser:              # any other board Claude reads in Chrome
    - name: my-board
      enabled: true
      url: https://example.com/jobs
      out: postings-my-board.json
      notes: "Engineering category, Remote US"
```

Remote Rocketship and Climatebase have built-in support (an API client and a URL builder). Any other board goes under `browser:`; boards focused on the sector you're interested in often turn up more relevant postings than general ones.

To add a board, add an entry under `browser:` with its URL and any notes on how to filter it. Keep `out` names matching `postings-*.json` so they stay gitignored. Run `./jobscan boards` to see what's turned on and which files exist.

## Using the CLI directly

```bash
./jobscan boards                 # list boards and their postings files
```

```bash
./jobscan climatebase-url        # filtered Climatebase URL from the config
```

```bash
./jobscan fetch-rr               # Remote Rocketship (needs RR_API_KEY)
```

```bash
./jobscan review-batch -top 30   # writes reviews/batch.json and reviews/criteria.md
```

```bash
./jobscan score -reviews reviews/reviews.json -batch reviews/batch.json
```

`score` and `review-batch` read the postings files of the boards that are turned on when you don't pass any files. Flags on `climatebase-url` and `fetch-rr` override the config. Run any command with `-h` to see its flags.

## Privacy

These files are gitignored. Keep them local:

- `config.yaml`
- `resume.md`
- `.env`
- `postings-*.json`
- `reviews/`
- `ranked.*`

Remote Rocketship's terms haven't been checked for storing or sharing its data, so don't share files built from it.

## Development

```bash
go vet ./... && go test ./...
```

Tests never read your real config or `.env`: `TestMain` points `JOBSCAN_CONFIG` at an empty file, and tests that need an env file use a temporary one.

The project files:

| File | Contents |
|---|---|
| `main.go` | Scoring |
| `rr.go` | Remote Rocketship client |
| `review.go` | Review batches and merging reviews |
| `boards.go` | Config lookup, boards and criteria |
| `.claude/skills/job-search/SKILL.md` | The skill |
| `.claude/agents/posting-reviewer.md` | The reviewer agent |
