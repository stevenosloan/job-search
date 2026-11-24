package climatebase

import (
	"net/url"
	"testing"
)

func TestClimatebaseURL(t *testing.T) {
	u, err := url.Parse(URL("101-250,251-500", "Climate Adaptation", true, false))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	want := map[string]string{
		"experience_levels": "Senior Level",
		"organization_size": "101-250|251-500",
		"sectors":           "Climate Adaptation",
		"organization_type": "Nonprofit",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if q.Get("categories") == "" {
		t.Error("categories missing")
	}
	if u.Host != "climatebase.org" || u.Path != "/jobs" {
		t.Errorf("url %s", u)
	}
}
