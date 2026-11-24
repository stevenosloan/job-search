---
name: job-search
description: Find and rank software engineering jobs against the user's resume and the criteria in their jobscan config, using the job boards that config enables (browser boards read by Claude, Remote Rocketship via API run by the user), then score with the jobscan Go tool. Use when the user asks to search for, collect, or rank job postings.
---

# Job search

Finds and ranks postings against the user's resume and the criteria they set in their config (what they want, what they won't do, which missions or industries interest them). The boards come from the user's config; then a keyword scoring pass and a Haiku review of the shortlist.

## The tool
`jobscan` is the user's small Go program in this repo (files: `main.go`, `rr.go`, `review.go`, `boards.go` and their `_test.go` files, `go.mod`, `go.sum`; one dependency, gopkg.in/yaml.v3; Go 1.21+). It has five commands:
- `jobscan boards [-files]` lists the configured boards: on/off, how each is collected, its postings file and whether that file exists. `-files` prints only the enabled boards' existing files.
- `jobscan climatebase-url [-sizes 101-250,251-500] [-sectors "..."] [-nonprofit] [-junior]` prints a filtered URL for Climatebase, a climate-sector board (off by default); defaults come from `boards.climatebase`.
- `jobscan fetch-rr [flags]` pulls Remote Rocketship postings through its API; flag defaults come from `boards.remote_rocketship`.
- `jobscan review-batch [-top 30] [-max-desc 6000] [-out reviews/batch.json] [postings.json ...]` writes the keyword top N, with truncated descriptions and `keyword_score`, for the reviewer agent, plus `criteria.md` beside it rendered from the config's `candidate` block.
- `jobscan score [-config f] [-out ranked] [-reviews a.json,b.json [-batch reviews/batch.json]] [postings.json ...]` dedupes, drops postings matching `exclude_terms`, scores and writes `ranked.md` / `ranked.csv`. With `-reviews`, reviewed postings rank first by the reviewer's `fit`, then the rest by keyword score.

With no postings files, score and review-batch read the enabled boards' files that exist.

Build with `go test ./... && go build -o jobscan .`. Tests run against an empty config (TestMain sets `JOBSCAN_CONFIG`) and must never read the user's own config. If the source is not in the session, ask the user to attach the files or connect the folder that holds them.

## Config (per user, not committed)
All criteria live in the config; the built-in defaults have no exclusions or mission terms. jobscan reads `-config`, then `$JOBSCAN_CONFIG`, then `./config.yaml` (gitignored), then `~/.config/jobscan/config.yaml`; with none it uses built-in defaults and says so. `config.example.yaml` is the committed template.
1. At the start of a run, run `./jobscan boards` and read the config. If there is none, copy `config.example.yaml` to `config.yaml` and fill it in with the user: their target roles, location, pay floor, company size, exclusions, the missions or industries that interest them (`candidate.interests` in order, and matching `mission_terms`), and which boards to use.
2. Confirm or update with the user: the `candidate` block (roles, strengths, not_a_fit, location, interests in order), `salary_floor`, `size_min`/`size_max`, `exclude_terms`, and which boards are enabled. Edit the user's config, never the example, for personal details.
3. Resume: ask for the file if it is not attached; use it to set `stack_terms` and `candidate.strengths`.
4. Boards: `boards.remote_rocketship` and `boards.climatebase` hold each source's filters and `out` file. `boards.browser` is a list of other boards (`name`, `url`, `out`, `notes`, `enabled`) that Claude reads in the browser; suggest boards that fit the user's interests and add them here. Keep `out` names matching `postings-*.json` so they stay gitignored. Collect only enabled boards.

## Remote Rocketship (API, run by the USER)
The workspace shell may not reach remoterocketship.com, and the API key is the user's. Never ask for, paste, or write the key, and never read, print, or edit `.env`.
1. Key setup (user, once): `cp .env.example .env && chmod 600 .env`, then put the key after `RR_API_KEY=` in their own editor. `.env` is gitignored. jobscan reads only `RR_API_KEY` from it, only when the environment variable is unset, and warns if the file is readable by others. `-env-file` points elsewhere; `-env-file ""` disables it.
   Then run: `./jobscan fetch-rr` (writes `boards.remote_rocketship.out`, default `postings-rr.json`). Skip it when the board is disabled.
   Flags override the config: `-titles`, `-locations`, `-seniority`, `-sizes "51,200;201,500"`, `-keywords` (AND), `-exclude`, `-tech`, `-industries`, `-min-salary`, `-max-pages`, `-max-jobs`, `-env-file`. The key never comes from a flag.
   Tests must never read the real `.env`: any test that clears `RR_API_KEY` passes `-env-file` with a temp path.
2. Salary shape: the spec types `salaryRange` as object|null with no defined fields, and the keyless `-sandbox` returns synthetic data with `salaryRange: null`, so it cannot show the real shape. After every run, fetch-rr prints "salaryRange shapes seen" (key set, count, one example) to stderr. On the first run, ask the user to paste those lines and check that the parser handles them. The parser reads min/from/low and max/to/high keys, converts hourly/weekly/monthly pay to annual, and treats non-USD pay as unknown unless the API's USD conversion is present. Status: checked 2026-11-23. Live shape is `{min, max, salaryType: "per year"|..., currencyCode, currencySymbol, minSalaryAsUSD, maxSalaryAsUSD, salaryHumanReadableText}` or null (~27% null). The parser prefers the `*AsUSD` values, reads the period from `salaryType`, and never lets `currencySymbol` "$" override `currencyCode`. Ids (`id`, `company.id`) come back as numbers.
3. Quota: 500 requests or 3,000 jobs per UTC day (whichever first), shared across keys. Defaults are conservative (6 pages x 50 jobs per size band, 1,000 jobs max); the tool stops on a 429 or when `X-JobLimit-Remaining` drops below 50, keeps what it has, and reports usage. Do not suggest looping around the limit.
4. Terms: fetch-rr writes results to `postings-rr.json`. Remote Rocketship's Terms have not been checked yet for local storage or sharing; until they are, keep the file local, don't share it, and don't add caching or seen-jobs persistence built on API results.
5. API facts: `POST /api/openclaw/jobs/`, `Authorization: Bearer`, `RR-API-Version: 1`; filters include `seniorityFilters` (entry-level, junior, mid, senior, expert), `companySizeFilters` (e.g. `51,200`; `201,500`), `minSalaryFilter`, `excludedKeywordFilters`, `industriesFilters`, `hideGhostJobsFilter`. Responses carry no company-size, posted-date, or structured salary fields, so size is taken from the band that was queried (the 51-200 band includes companies under 150) and should be verified before recommending.
6. Supply note (2026-11-23): a broad query (3 titles, US, senior/expert, 2 size bands) hits the 600-job page cap, mostly with companies outside a niche interest, and narrow `-keywords` can return only a handful. For a niche interest, a board focused on that sector is usually a better primary source. Short mission terms match noise ("land" in Maryland, "ecosystem" in crypto), so don't trust the mission column without reading the posting.
7. Read `postings-rr.json` once the user shares it.

## Climatebase (browser, run by Claude, when enabled)
Climatebase, a climate-sector board, is behind a bot check that only a real signed-in browser passes, and the shell cannot reach it. Never complete or bypass a verification challenge; if one appears, ask the user to complete it.
1. Use Claude in Chrome if connected (else the built-in browser). Open a NEW tab; close it when done.
2. Navigate to the URL from `./jobscan climatebase-url` (flags override the config), wait ~3s, `get_page_text`. The search box and `query=` param do NOT filter; the URL filters do (`experience_levels`, `categories`, `sectors`, `organization_size` with `|` between values, `organization_type`).
3. Sector names (for `boards.climatebase.sectors`) must match the site's dropdown exactly, e.g. `Food, Agriculture, & Land Use`, `Climate Adaptation`, `Research & Education`.
4. The size filter is loose; verify on each job page ("About the Company" shows the band). Job pages are `https://climatebase.org/job/<id>/<slug>`; get hrefs with `read_page` on a listing ref. List pages are long: shortlist by title/company first, then open only promising listings for pay, size, stack, responsibilities.
5. Record postings as JSON (`title, company, location, remote, salary_min, salary_max, size, url, source:"climatebase", description`) in the board's `out` file (default `postings-cb.json`). Use real values; omit unknown fields. Salary may be a number or a string like "$165k".

## Other browser boards (`boards.browser`)
For each enabled entry, open its `url` in a new tab and follow its `notes`, with the same rules as above: never complete or bypass a verification challenge, shortlist by title first, verify size and pay on the posting, and record postings in the entry's `out` file with `source` set to its `name`.

## Score and report
1. `./jobscan score -out ranked` (uses the enabled boards' files; list files to override. Config keys: `salary_floor`, `size_min`, `size_max`, `exclude_terms`, `mission_terms`, `product_terms`, `growth_terms`, `stack_terms`, `senior_terms`, `mission_weight`, `no_mission_penalty`, `candidate`, `boards`; JSON also parses; missing keys keep defaults, unknown keys are an error). Terms match at the start of a word ("soil" matches "soils", "land" matches "landing"): prefer phrases that can't turn up in unrelated text.
2. The keyword score is a cheap first pass. Review the shortlist with the `posting-reviewer` agent (`.claude/agents/posting-reviewer.md`, model haiku, tools Read and Write):
   - `./jobscan review-batch -top 30 -out reviews/batch.json` (same inputs as step 1). It fails if the config lacks `candidate.location` or `candidate.interests`.
   - Run the agent with the batch path, `reviews/criteria.md`, `resume.md`, and `reviews/reviews.json` as the output. If the agent type is not registered in the session (it loads at session start), run a general-purpose agent with `model: haiku` and tell it to read and follow that file. Above ~40 postings, split into batches of 30 and run them in parallel, one output file each.
   - `./jobscan score -out ranked -reviews reviews/reviews.json -batch reviews/batch.json` (comma-separate several review files). Always pass `-batch`: the reviewer can report success while writing fewer objects or altering a URL. When postings lack a review, score lists them and writes `reviews/batch-missing.json`; run the reviewer on that file into `reviews/reviews-missing.json`, then re-run score with `-reviews reviews/reviews.json,reviews/reviews-missing.json` until it reports full coverage. A separate warning lists reviews whose URL matched no posting (usually an altered URL: drop that entry; its posting shows up as missing).
   - `reviews/` is gitignored and holds Remote Rocketship-derived text: keep it local like `postings-rr.json`, and don't reuse old reviews to skip postings on later runs.
3. Haiku's verdicts are a second opinion, not verification. Read the top results against the resume yourself, spot-check the reviewer's claims (location, pay, stack) against the posting, verify size/pay on the source page, and say what is unverified. Known slip: it marks "Remote within the Americas" style locations as unknown.
4. Report a short ranked list: role, company, size, pay, why it fits, gaps vs the resume, deadline if any, link. Be candid when supply is thin or pay is under the floor.
