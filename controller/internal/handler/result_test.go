package handler

import (
	"net/http/httptest"
	"testing"
)

func TestParseResultListParamsAcceptsQAlias(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/result/list?limit=8&offset=16&q=playwright_native_arrival", nil)

	limit, offset, search := parseResultListParams(req)
	if limit != 8 {
		t.Fatalf("expected limit 8, got %d", limit)
	}
	if offset != 16 {
		t.Fatalf("expected offset 16, got %d", offset)
	}
	if search != "playwright_native_arrival" {
		t.Fatalf("expected q alias to be used as search, got %q", search)
	}
}

func TestParseResultListParamsPrefersExplicitSearch(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/result/list?search=direct&q=ignored", nil)

	_, _, search := parseResultListParams(req)
	if search != "direct" {
		t.Fatalf("expected explicit search param to win, got %q", search)
	}
}
