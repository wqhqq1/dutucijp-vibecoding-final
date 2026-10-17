package web

import (
	"encoding/csv"
	"net/url"
	"strings"
	"testing"

	"returnsrecon/internal/store"
)

func TestHTTP_TC16_CountrySortPaginationAndExportAgree(t *testing.T) {
	f := httpSecurityTest(t)
	finance := f.configured(t, "http-finance")
	base := "/api/transactions?run_id=" + f.run
	read := func(suffix string) store.TransactionPage {
		w := f.request(t, finance, "GET", base+suffix, nil, nil)
		expectHTTPStatus(t, w, 200)
		var page store.TransactionPage
		decodeHTTPResponse(t, w, &page)
		return page
	}
	first := read("&country=UK&sort=invoice&direction=desc&page_size=2")
	second := read("&country=UK&sort=invoice&direction=desc&page_size=2&page=2")
	if first.Total != 6 || second.Total != 6 || len(first.Items) != 2 || first.Items[0].ID != "0:7" || first.Items[1].ID != "0:6" || second.Items[0].ID != "0:5" {
		t.Fatal("HTTP country, descending sort, or page parameters were not applied")
	}
	if page := read("&country=France"); page.Total != 0 || len(page.Items) != 0 {
		t.Fatal("country filter leaked rows outside the exact country")
	}
	w := f.request(t, finance, "GET", "/api/exports?run_id="+f.run+"&country=UK&sort=invoice&direction=desc&page_size=2", nil, nil)
	expectHTTPStatus(t, w, 200)
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(w.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(records) != 7 {
		t.Fatal("CSV must include all six filtered rows, irrespective of the page size")
	}
	column := -1
	for i, header := range records[0] {
		if header == "row_id" {
			column = i
		}
	}
	if column < 0 || records[1][column] != first.Items[0].ID || records[3][column] != second.Items[0].ID {
		t.Fatal("HTTP export sort differs from the displayed pages")
	}
	for _, query := range []string{"&sort=" + url.QueryEscape("invoice;DROP TABLE normalized_rows"), "&direction=sideways"} {
		expectHTTPStatus(t, f.request(t, finance, "GET", base+query, nil, nil), 422)
	}
}
