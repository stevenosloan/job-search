package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestFetchRRPaginatesAndMaps(t *testing.T) {
	pages := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" || r.Header.Get("RR-API-Version") != "1" {
			w.WriteHeader(401)
			w.Write([]byte(`{"message":"no","code":"unauthorized","hint":"key"}`))
			return
		}
		var body struct {
			Filters map[string]any `json:"filters"`
			Desc    bool           `json:"includeJobDescription"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if !body.Desc || body.Filters["minSalaryFilter"].(float64) != 170000 {
			t.Errorf("bad body: %+v", body)
		}
		pages++
		page := int(body.Filters["page"].(float64))
		w.Header().Set("X-JobLimit-Remaining", "2900")
		desc := "soil health, React"
		resp := map[string]any{
			"jobOpenings": []map[string]any{{"id": page*10 + 1, "roleTitle": "Senior Engineer", "url": "https://x/1",
				"slug": "s", "roleDescription": desc, "roleRequirements": nil,
				"company":     map[string]any{"id": "c1", "name": "AcmeSoil", "slug": "acme"},
				"salaryRange": map[string]any{"min": 150000, "max": 190000, "currency": "USD"}}},
			"totalCount": 2,
			"pagination": map[string]any{"page": page, "itemsPerPage": 50, "totalCount": 2, "totalPages": 2,
				"hasNextPage": page < 2, "hasPreviousPage": page > 1},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()
	t.Setenv("RR_BASE_URL", srv.URL)
	t.Setenv("RR_API_KEY", "test-key")
	out := filepath.Join(t.TempDir(), "rr.json")
	if err := runFetchRR([]string{"-sizes", "201,500", "-out", out}); err != nil {
		t.Fatal(err)
	}
	var jobs []Job
	b, _ := os.ReadFile(out)
	if err := json.Unmarshal(b, &jobs); err != nil || len(jobs) != 2 {
		t.Fatalf("jobs=%d err=%v", len(jobs), err)
	}
	if jobs[0].Company != "AcmeSoil" || jobs[0].Size != "201-500" || jobs[0].Source != "remoterocketship" {
		t.Errorf("mapping: %+v", jobs[0])
	}
	if lo, ok := money(jobs[0].SalaryMin); !ok || lo != 150000 {
		t.Errorf("salary min: %v %v", lo, ok)
	}
	if hi, _ := money(jobs[0].SalaryMax); hi != 190000 {
		t.Errorf("salary max: %v", hi)
	}
	if pages != 2 {
		t.Errorf("expected 2 pages, got %d", pages)
	}
}

func TestFetchRRHandles429AndMissingKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		w.Write([]byte(`{"message":"slow","code":"rate_limited","hint":"wait"}`))
	}))
	defer srv.Close()
	t.Setenv("RR_BASE_URL", srv.URL)
	t.Setenv("RR_API_KEY", "")
	noEnv := filepath.Join(t.TempDir(), "absent.env") // never read the real ./.env in tests
	if err := runFetchRR([]string{"-env-file", noEnv, "-out", filepath.Join(t.TempDir(), "x.json")}); err == nil {
		t.Error("expected missing-key error")
	}
	t.Setenv("RR_API_KEY", "k")
	out := filepath.Join(t.TempDir(), "y.json")
	if err := runFetchRR([]string{"-env-file", noEnv, "-out", out}); err != nil {
		t.Fatalf("429 should end gracefully, got %v", err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Error("output file should still be written")
	}
}

func TestLoadKeyFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	os.WriteFile(p, []byte("# comment\nOTHER=x\nexport RR_API_KEY=\"from-file\"\n"), 0o600)

	t.Setenv("RR_API_KEY", "")
	if err := loadKeyFromDotEnv(p); err != nil || os.Getenv("RR_API_KEY") != "from-file" {
		t.Errorf("load: err=%v got=%q", err, os.Getenv("RR_API_KEY"))
	}
	if os.Getenv("OTHER") != "" {
		t.Error("only RR_API_KEY should be loaded")
	}
	t.Setenv("RR_API_KEY", "from-env") // environment wins
	loadKeyFromDotEnv(p)
	if os.Getenv("RR_API_KEY") != "from-env" {
		t.Error("environment should take precedence over .env")
	}
	t.Setenv("RR_API_KEY", "")
	if err := loadKeyFromDotEnv(filepath.Join(dir, "missing.env")); err != nil || os.Getenv("RR_API_KEY") != "" {
		t.Errorf("missing file should be a no-op: %v", err)
	}
}

func TestSalaryFromRawShapes(t *testing.T) {
	cases := map[string][2]string{
		`{"min":120000,"max":160000}`: {"120000", "160000"},
		`"$120k - $160k"`:             {"120000", "160000"},
		`null`:                        {"", ""},
		`{"max":150000,"min":90000}`:  {"90000", "150000"},
		// distractor numbers must not be read as pay
		`{"id":7,"min":170000,"max":210000,"currency":"USD","year":2026}`: {"170000", "210000"},
		// hourly / monthly normalize to annual (2080 h, 12 mo)
		`{"min":80,"max":100,"currency":"USD","period":"hourly"}`: {"166400", "208000"},
		`{"minValue":15000,"maxValue":18000,"unit":"MONTH"}`:      {"180000", "216000"},
		// non-USD is unknown rather than a misleading number
		`{"min":90000,"max":120000,"currency":"EUR"}`: {"", ""},
		// nested and string-valued amounts
		`{"salary":{"from":"$150k","to":"$190k"}}`: {"150000", "190000"},
		`{"max":200000}`: {"200000", ""},
		`{}`:             {"", ""},
	}
	for in, want := range cases {
		lo, hi := salaryFromRaw(json.RawMessage(in))
		if lo != want[0] || hi != want[1] {
			t.Errorf("%s -> %s,%s want %v", in, lo, hi, want)
		}
	}
}
