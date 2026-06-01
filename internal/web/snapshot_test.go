package web

import (
	"net/http"
	"net/http/httputil"
	"strings"
	"testing"

	"github.com/beme/abide"
)

type snapshotString string

func (s snapshotString) String() string {
	return string(s)
}

func assertHTTPResponse(t *testing.T, id string, response *http.Response) {
	t.Helper()

	body, err := httputil.DumpResponse(response, true)
	if err != nil {
		t.Fatal(err)
	}

	abide.Assert(t, id, snapshotString(strings.ReplaceAll(string(body), "\r\n", "\n")))
}
